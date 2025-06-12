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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiv1alpha1 "github.com/crazyfrankie/autodeploy-scaler/api/v1alpha1"
)

var _ = Describe("Scaler Controller", func() {
	var (
		ctx                  context.Context
		scaler               *apiv1alpha1.Scaler
		deployment           *appsv1.Deployment
		controllerReconciler *ScalerReconciler
		scalerKey            types.NamespacedName
		deploymentKey        types.NamespacedName
		testNamespace        string
		resourceSuffix       string
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Use timestamp to make resource names unique
		resourceSuffix = fmt.Sprintf("%d", time.Now().UnixNano())
		testNamespace = "default"

		scalerName := fmt.Sprintf("test-scaler-%s", resourceSuffix)
		deploymentName := fmt.Sprintf("test-deployment-%s", resourceSuffix)

		scalerKey = types.NamespacedName{Name: scalerName, Namespace: testNamespace}
		deploymentKey = types.NamespacedName{Name: deploymentName, Namespace: testNamespace}

		controllerReconciler = &ScalerReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		// Clear the global maps before each test
		originalDeploymentInfo = make(map[string]apiv1alpha1.DeploymentInfo)
		annotations = make(map[string]string)

		// Create a test deployment
		deployment = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: testNamespace,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To(int32(3)),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": fmt.Sprintf("test-%s", resourceSuffix)},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": fmt.Sprintf("test-%s", resourceSuffix)},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "nginx:latest",
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
	})

	AfterEach(func() {
		// Clean up scaler if it exists and wait for deletion
		scaler := &apiv1alpha1.Scaler{}
		if err := k8sClient.Get(ctx, scalerKey, scaler); err == nil {
			// Remove finalizer first to allow deletion
			scaler.Finalizers = []string{}
			k8sClient.Update(ctx, scaler)
			k8sClient.Delete(ctx, scaler)

			// Wait for deletion to complete
			Eventually(func() bool {
				err := k8sClient.Get(ctx, scalerKey, scaler)
				return err != nil
			}, time.Second*5, time.Millisecond*100).Should(BeTrue())
		}

		// Clean up deployment if it exists and wait for deletion
		deployment := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, deploymentKey, deployment); err == nil {
			k8sClient.Delete(ctx, deployment)

			// Wait for deletion to complete
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				return err != nil
			}, time.Second*5, time.Millisecond*100).Should(BeTrue())
		}
	})

	Context("Basic Scaler functionality", func() {
		It("should create a scaler and add finalizer", func() {
			By("Creating a new Scaler")
			currentHour := time.Now().Local().Hour()
			// Set time range that doesn't include current hour to avoid scaling
			startTime := (currentHour + 1) % 24
			endTime := (currentHour + 2) % 24

			scaler = &apiv1alpha1.Scaler{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scalerKey.Name,
					Namespace: scalerKey.Namespace,
				},
				Spec: apiv1alpha1.ScalerSpec{
					StartTime: startTime,
					EndTime:   endTime,
					Replicas:  5,
					Deployment: []apiv1alpha1.DeploymentSpec{
						{
							Name:      deploymentKey.Name,
							Namespace: deploymentKey.Namespace,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scaler)).To(Succeed())

			By("Reconciling the created resource")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: scalerKey,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			By("Checking the scaler was updated")
			err = k8sClient.Get(ctx, scalerKey, scaler)
			Expect(err).NotTo(HaveOccurred())

			// Check finalizer was added
			Expect(scaler.Finalizers).To(ContainElement(finalizer))

			// Check status was set
			Expect(scaler.Status.Status).To(Equal(apiv1alpha1.PENDING))
		})

		It("should scale deployment when in time window", func() {
			By("Creating a scaler with current time in window")
			currentHour := time.Now().Local().Hour()
			// Set time range that includes current hour
			startTime := currentHour
			endTime := (currentHour + 1) % 24

			scaler = &apiv1alpha1.Scaler{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scalerKey.Name,
					Namespace: scalerKey.Namespace,
				},
				Spec: apiv1alpha1.ScalerSpec{
					StartTime: startTime,
					EndTime:   endTime,
					Replicas:  5,
					Deployment: []apiv1alpha1.DeploymentSpec{
						{
							Name:      deploymentKey.Name,
							Namespace: deploymentKey.Namespace,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scaler)).To(Succeed())

			By("Reconciling to trigger scaling")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: scalerKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying deployment was scaled")
			err = k8sClient.Get(ctx, deploymentKey, deployment)
			Expect(err).NotTo(HaveOccurred())
			Expect(*deployment.Spec.Replicas).To(Equal(int32(5)))

			By("Verifying scaler status")
			err = k8sClient.Get(ctx, scalerKey, scaler)
			Expect(err).NotTo(HaveOccurred())
			Expect(scaler.Status.Status).To(Equal(apiv1alpha1.SCALED))
		})

		It("should handle missing deployment gracefully", func() {
			By("Creating a scaler with non-existent deployment")
			currentHour := time.Now().Local().Hour()
			startTime := currentHour
			endTime := (currentHour + 1) % 24

			scaler = &apiv1alpha1.Scaler{
				ObjectMeta: metav1.ObjectMeta{
					Name:      scalerKey.Name,
					Namespace: scalerKey.Namespace,
				},
				Spec: apiv1alpha1.ScalerSpec{
					StartTime: startTime,
					EndTime:   endTime,
					Replicas:  5,
					Deployment: []apiv1alpha1.DeploymentSpec{
						{
							Name:      "nonexistent-deployment",
							Namespace: scalerKey.Namespace,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scaler)).To(Succeed())

			By("First reconcile to add finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: scalerKey,
			})
			Expect(err).NotTo(HaveOccurred()) // First reconcile should succeed

			By("Second reconcile should fail when trying to process missing deployment")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: scalerKey,
			})
			Expect(err).To(HaveOccurred()) // Second reconcile should fail
		})
	})
})

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
