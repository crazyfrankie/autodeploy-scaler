/*
Copyright 2025.

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
	"fmt"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/crazyfrankie/autodeploy-scaler/api/v1alpha1"
)

var (
	logger = logf.Log.WithName("scaler_controller")
)

// ScalerReconciler reconciles a Scaler object
type ScalerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=api.crazyfrank.com,resources=scalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=api.crazyfrank.com,resources=scalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=api.crazyfrank.com,resources=scalers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Scaler object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *ScalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	log.Info("Reconcile called")

	// create a scaler object
	scaler := &apiv1alpha1.Scaler{}
	err := r.Get(ctx, req.NamespacedName, scaler)
	if err != nil {
		// if not found scaler object, use IgnoreNotFound to leave the process uninterrupted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	startTime := scaler.Spec.StartTime
	endTime := scaler.Spec.EndTime
	replicas := scaler.Spec.Replicas

	currentHour := time.Now().Local().Hour()
	log.Info(fmt.Sprintf("Current Time: %d", currentHour))

	// from startTime end to endTime
	if currentHour >= startTime && currentHour < endTime {
		for _, deploy := range scaler.Spec.Deployment {
			deployment := &v1.Deployment{}
			err := r.Get(ctx, types.NamespacedName{
				Namespace: deploy.Namespace,
				Name:      deploy.Name,
			}, deployment)
			if err != nil {
				return ctrl.Result{}, err
			}

			// Determine if the number of replicas in a cluster deployment is
			// equal to the required number of replicas in a cluster deployment.
			if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != replicas {
				*deployment.Spec.Replicas = replicas
				err := r.Update(ctx, deployment)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiv1alpha1.Scaler{}).
		Named("scaler").
		Complete(r)
}
