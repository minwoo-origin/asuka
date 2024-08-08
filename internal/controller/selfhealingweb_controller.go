/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ctrl "sigs.k8s.io/controller-runtime"

	appv1 "web.test/asuka/api/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"	
)

// SelfhealingWebReconciler reconciles a SelfhealingWeb object
type SelfhealingWebReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	stopCh chan struct{}
}

// +kubebuilder:rbac:groups=app.web.test,resources=selfhealingwebs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.web.test,resources=selfhealingwebs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.web.test,resources=selfhealingwebs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SelfhealingWeb object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *SelfhealingWebReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	
	var selfhealingWeb appv1.SelfhealingWeb
	if err := r.Get(ctx, req.NamespacedName, &selfhealingWeb); err != nil {
		log.Error(err, "Error Getting SelfhealingWeb")
		return ctrl.Result{}, err
	}

	// Pod Scailing
	desiredReplicas := selfhealingWeb.Spec.Replicas
	currentPods := &corev1.PodList{}
	if err := r.List(ctx, currentPods, client.InNamespace(req.Namespace), client.MatchingLabels{"app": selfhealingWeb.Name}); err != nil {
		log.Error(err, "Error Listing Pods")
		return ctrl.Result{}, err
	}

	currentReplicas := int32(len(currentPods.Items))
	if currentReplicas < desiredReplicas {
		for i := currentReplicas; i < desiredReplicas; i++ {
			log.Info("Creating Pod")
			pod := NewPod(&selfhealingWeb)
			if err := controllerutil.SetControllerReference(&selfhealingWeb, pod, r.Scheme); err != nil {
				log.Error(err, "Error Setting Controller Reference")
				return ctrl.Result{}, err
			}
			if err := r.Create(ctx, pod); err != nil {
				log.Error(err, "Error Creating Pod")
				return ctrl.Result{}, err
			}
		}
	} else if currentReplicas > desiredReplicas {
		for i := currentReplicas - 1; i >= desiredReplicas; i-- {
			log.Info("Deleting Pod")
			pod := currentPods.Items[i]
			if err := r.Delete(ctx, &pod); err != nil {
				log.Error(err, "Error Deleting Pod")
				return ctrl.Result{}, err
			}
		}
	}
	
	return ctrl.Result{}, nil
}

// Create Pod
func NewPod(cr *appv1.SelfhealingWeb) *corev1.Pod {	
	labels := map[string]string{
		"app": cr.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: cr.Name + "-",
			Namespace:    cr.Namespace,
			Labels:       labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  cr.Name,
					Image: "nginx",
				},
			},
			RestartPolicy: corev1.RestartPolicyAlways,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SelfhealingWebReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	log := log.FromContext(ctx)
	log.Info("SetupWithManager Called")

	if r.stopCh == nil {
		r.stopCh = make(chan struct{})
		go r.Watcher()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1.SelfhealingWeb{}).
		Complete(r)
}

// Periodic Watcher
func (r *SelfhealingWebReconciler) Watcher() {
	ctx := context.Background()
	log := log.FromContext(ctx)
	log.Info("Running Periodic Task")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// Update the PodStatus
			log.Info("Watcher Running")
			var selfhealingWebs appv1.SelfhealingWebList
			if err := r.List(ctx, &selfhealingWebs); err != nil {
				log.Error(err, "Error Listing SelfhealingWebs")
				continue
			}

			for _, selfhealingWeb := range selfhealingWebs.Items {
				selfhealingWeb.Status.WatcherStatus = []appv1.PodStatus{}
				label := map[string]string{
					"app": selfhealingWeb.Name,
				}
				var pods corev1.PodList
				if err := r.List(ctx, &pods, client.MatchingLabels(label)); err != nil {
					log.Error(err, "Error Listing Pods")
					continue
				}
				// Check the API
				for _, pod := range pods.Items {
					statusCode := checkAPI(pod)
					selfhealingWeb.Status.WatcherStatus = append(selfhealingWeb.Status.WatcherStatus, appv1.PodStatus{
						PodName: pod.Name,
						PodStatus: string(pod.Status.Phase),
						PodStatusCode: statusCode,
					})
				}
				// Evaluate the HealthStatus
				allRunning := true
				allServiceAvailable := true
				for _, podStatus := range selfhealingWeb.Status.WatcherStatus {
					if podStatus.PodStatusCode != http.StatusOK {
						allServiceAvailable = false
						break
					if podStatus.PodStatus != "Running" {
						allRunning = false
						break
					}
				}
				if allRunning && allServiceAvailable {					// All Pods are Running and Service is Available
					selfhealingWeb.Status.HealthStatus = "Healthy"
				} else if allRunning {									// All Pods are Running but Service is Unavailable
					selfhealingWeb.Status.HealthStatus = "Warning"
				} else {												// Some Pods are not Running			
					selfhealingWeb.Status.HealthStatus = "Critical"
				}
			}

		case <-r.stopCh:
			return
		}
	}
}

// Check API
func checkAPI(pod corev1.Pod) int {
	resp, err := http.Get("http://" + pod.Status.PodIP)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	defer resp.Body.Close()
	result := resp.StatusCode
	return result
}