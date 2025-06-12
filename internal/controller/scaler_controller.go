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
	"time"

	"github.com/bytedance/sonic"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/crazyfrankie/autodeploy-scaler/api/v1alpha1"
)

const (
	finalizer = "scalers.api.crazyfrank.com/finalizer"
)

var (
	logger                 = logf.Log.WithName("scaler_controller")
	originalDeploymentInfo = make(map[string]apiv1alpha1.DeploymentInfo)
	annotations            = make(map[string]string)
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

	// if not be deleted then add finalizer
	if scaler.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(scaler, finalizer) {
			// add finalizer
			controllerutil.AddFinalizer(scaler, finalizer)
			log.Info("add finalizer.")
			if err := r.Update(ctx, scaler); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}

		if scaler.Status.Status == "" {
			scaler.Status.Status = apiv1alpha1.PENDING
			if err := r.Status().Update(ctx, scaler); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// add scaler's deployments' info add to annotations
			if err := r.addAnnotations(ctx, scaler); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}

		// start execute scaler's logic
		startTime := scaler.Spec.StartTime
		endTime := scaler.Spec.EndTime
		replicas := scaler.Spec.Replicas

		currentHour := time.Now().Local().Hour()
		log.Info(fmt.Sprintf("Current Time: %d", currentHour))

		// from startTime end to endTime
		if currentHour >= startTime && currentHour < endTime {
			if scaler.Status.Status != apiv1alpha1.SCALED {
				log.Info("Starting Call scaleDeployment func.")
				err := r.scaleDeployment(ctx, scaler, replicas)
				if err != nil {
					return ctrl.Result{}, err
				}
			} else {
				if scaler.Status.Status == apiv1alpha1.SCALED {
					if err := r.restoreDeployment(ctx, scaler); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		} else {
			if scaler.Status.Status == apiv1alpha1.SCALED {
				err := r.restoreDeployment(ctx, scaler)
				if err != nil {
					return ctrl.Result{}, client.IgnoreNotFound(err)
				}
			}
		}
	} else {
		// start delete
		log.Info("start deletion flow.")
		if scaler.Status.Status == apiv1alpha1.SCALED {
			if err := r.restoreDeployment(ctx, scaler); err != nil {
				return ctrl.Result{}, err
			}
		}
		log.Info("remove finalizer.")
		controllerutil.RemoveFinalizer(scaler, finalizer)
		if err := r.Update(ctx, scaler); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("remove scaler.")
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *ScalerReconciler) addAnnotations(ctx context.Context, scaler *apiv1alpha1.Scaler) error {
	// record deployments' original replicas and namespace
	for _, deploy := range scaler.Spec.Deployment {
		deployment := &v1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: deploy.Namespace,
			Name:      deploy.Name,
		}, deployment); err != nil {
			return err
		}

		if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas != scaler.Spec.Replicas {
			logger.Info("add original state to originalDeploymentInfo map.")
			info := apiv1alpha1.DeploymentInfo{
				Namespace: deployment.Namespace,
				Replicas:  *deployment.Spec.Replicas,
			}
			originalDeploymentInfo[deployment.Name] = info

			infoJson, err := sonic.MarshalString(&info)
			if err != nil {
				return err
			}
			annotations[deployment.Name] = infoJson
		}
	}

	scaler.Annotations = annotations

	// update scaler annotations
	if err := r.Update(ctx, scaler); err != nil {
		return err
	}

	return nil
}

func (r *ScalerReconciler) restoreDeployment(ctx context.Context, scaler *apiv1alpha1.Scaler) error {
	logger.Info("starting to return to the original state.")

	for name, originalDeployInfo := range originalDeploymentInfo {
		deployment := &v1.Deployment{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: originalDeployInfo.Namespace,
			Name:      name,
		}, deployment)
		if err != nil {
			return err
		}

		if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas != originalDeployInfo.Replicas {
			*deployment.Spec.Replicas = originalDeployInfo.Replicas
			if err := r.Update(ctx, deployment); err != nil {
				return err
			}
		}
	}

	scaler.Status.Status = apiv1alpha1.RESTORED
	if err := r.Update(ctx, scaler); err != nil {
		return err
	}

	return nil
}

func (r *ScalerReconciler) scaleDeployment(ctx context.Context, scaler *apiv1alpha1.Scaler, replicas int32) error {
	for _, deploy := range scaler.Spec.Deployment {
		deployment := &v1.Deployment{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: deploy.Namespace,
			Name:      deploy.Name,
		}, deployment)
		if err != nil {
			return err
		}

		// Determine if the number of replicas in a cluster deployment is
		// equal to the required number of replicas in a cluster deployment.
		if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != replicas {
			*deployment.Spec.Replicas = replicas
			err := r.Update(ctx, deployment)
			if err != nil {
				scaler.Status.Status = apiv1alpha1.FAILED
				r.Status().Update(ctx, scaler)
				return err
			}
			scaler.Status.Status = apiv1alpha1.SCALED
			r.Status().Update(ctx, scaler)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiv1alpha1.Scaler{}).
		Named("scaler").
		Complete(r)
}
