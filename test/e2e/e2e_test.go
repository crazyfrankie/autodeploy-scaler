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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/crazyfrankie/autodeploy-scaler/test/utils"
)

// namespace where the project is deployed in
const namespace = "autodelpoy-scaler-system"

// serviceAccountName created for the project
const serviceAccountName = "autodelpoy-scaler-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "autodelpoy-scaler-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "autodelpoy-scaler-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=autodelpoy-scaler-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccount": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput := getMetricsOutput()
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))

		It("should create and manage Scaler resources successfully", func() {
			By("creating a test deployment")
			testDeployment := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-app
  template:
    metadata:
      labels:
        app: test-app
    spec:
      containers:
      - name: nginx
        image: nginx:latest
        ports:
        - containerPort: 80
`
			deploymentFile := "/tmp/test-deployment.yaml"
			err := os.WriteFile(deploymentFile, []byte(testDeployment), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd := exec.Command("kubectl", "apply", "-f", deploymentFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for deployment to be ready")
			Eventually(func() bool {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment", "-o", "jsonpath={.status.readyReplicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return false
				}
				return output == "2"
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			By("creating a Scaler resource")
			// Get current hour and set scaling window to test immediate scaling
			currentTime := time.Now()
			currentHour := currentTime.Hour()
			startTime := currentHour
			endTime := (currentHour + 1) % 24

			scalerYAML := fmt.Sprintf(`
apiVersion: api.crazyfrank.com/v1alpha1
kind: Scaler
metadata:
  name: test-scaler
  namespace: default
spec:
  startTime: %d
  endTime: %d
  replicas: 5
  deployment:
  - name: test-deployment
    namespace: default
`, startTime, endTime)

			scalerFile := "/tmp/test-scaler.yaml"
			err = os.WriteFile(scalerFile, []byte(scalerYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", scalerFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying scaler status becomes SCALED")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "scaler", "test-scaler", "-o", "jsonpath={.status.status}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("Scaled"))

			By("verifying deployment is scaled to target replicas")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("5"))

			By("verifying scaler has finalizer")
			cmd = exec.Command("kubectl", "get", "scaler", "test-scaler", "-o", "jsonpath={.metadata.finalizers}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("scalers.api.crazyfrank.com/finalizer"))

			By("verifying scaler has annotations with original deployment info")
			cmd = exec.Command("kubectl", "get", "scaler", "test-scaler", "-o", "jsonpath={.metadata.annotations}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("test-deployment"))

			By("cleaning up test resources")
			cmd = exec.Command("kubectl", "delete", "scaler", "test-scaler")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying deployment is restored to original replicas after scaler deletion")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("2"))

			By("verifying scaler is completely deleted")
			Eventually(func() bool {
				cmd := exec.Command("kubectl", "get", "scaler", "test-scaler")
				_, err := utils.Run(cmd)
				return err != nil // Should return error when not found
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			cmd = exec.Command("kubectl", "delete", "deployment", "test-deployment")
			_, _ = utils.Run(cmd)
		})

		It("should handle multiple deployments correctly", func() {
			By("creating multiple test deployments")
			deployment1YAML := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment-1
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: test-app-1
  template:
    metadata:
      labels:
        app: test-app-1
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`

			deployment2YAML := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment-2
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test-app-2
  template:
    metadata:
      labels:
        app: test-app-2
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`

			deployment1File := "/tmp/test-deployment-1.yaml"
			deployment2File := "/tmp/test-deployment-2.yaml"

			err := os.WriteFile(deployment1File, []byte(deployment1YAML), 0644)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(deployment2File, []byte(deployment2YAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd := exec.Command("kubectl", "apply", "-f", deployment1File)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", deployment2File)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for deployments to be ready")
			Eventually(func() bool {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-1", "-o", "jsonpath={.status.readyReplicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return false
				}
				return output == "3"
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			Eventually(func() bool {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-2", "-o", "jsonpath={.status.readyReplicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return false
				}
				return output == "1"
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			By("creating a Scaler for multiple deployments")
			currentTime := time.Now()
			currentHour := currentTime.Hour()
			startTime := currentHour
			endTime := (currentHour + 1) % 24

			multiScalerYAML := fmt.Sprintf(`
apiVersion: api.crazyfrank.com/v1alpha1
kind: Scaler
metadata:
  name: multi-scaler
  namespace: default
spec:
  startTime: %d
  endTime: %d
  replicas: 4
  deployment:
  - name: test-deployment-1
    namespace: default
  - name: test-deployment-2
    namespace: default
`, startTime, endTime)

			multiScalerFile := "/tmp/multi-scaler.yaml"
			err = os.WriteFile(multiScalerFile, []byte(multiScalerYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", multiScalerFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying both deployments are scaled")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-1", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("4"))

			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-2", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("4"))

			By("cleaning up resources")
			cmd = exec.Command("kubectl", "delete", "scaler", "multi-scaler")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying deployments are restored to original replicas")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-1", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("3"))

			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-2", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 2*time.Minute, 5*time.Second).Should(Equal("1"))

			cmd = exec.Command("kubectl", "delete", "deployment", "test-deployment-1")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "deployment", "test-deployment-2")
			_, _ = utils.Run(cmd)
		})

		It("should not scale when outside time window", func() {
			By("creating a test deployment")
			testDeployment := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment-out-window
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-app-out-window
  template:
    metadata:
      labels:
        app: test-app-out-window
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`
			deploymentFile := "/tmp/test-deployment-out-window.yaml"
			err := os.WriteFile(deploymentFile, []byte(testDeployment), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd := exec.Command("kubectl", "apply", "-f", deploymentFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating a Scaler with time window that excludes current time")
			currentTime := time.Now()
			currentHour := currentTime.Hour()
			// Set window to be outside current hour
			startTime := (currentHour + 2) % 24
			endTime := (currentHour + 3) % 24

			scalerYAML := fmt.Sprintf(`
apiVersion: api.crazyfrank.com/v1alpha1
kind: Scaler
metadata:
  name: out-window-scaler
  namespace: default
spec:
  startTime: %d
  endTime: %d
  replicas: 5
  deployment:
  - name: test-deployment-out-window
    namespace: default
`, startTime, endTime)

			scalerFile := "/tmp/out-window-scaler.yaml"
			err = os.WriteFile(scalerFile, []byte(scalerYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", scalerFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying scaler status becomes PENDING")
			Eventually(func() string {
				cmd := exec.Command("kubectl", "get", "scaler", "out-window-scaler", "-o", "jsonpath={.status.status}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 1*time.Minute, 5*time.Second).Should(Equal("Pending"))

			By("verifying deployment maintains original replicas")
			Consistently(func() string {
				cmd := exec.Command("kubectl", "get", "deployment", "test-deployment-out-window", "-o", "jsonpath={.spec.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return ""
				}
				return output
			}, 30*time.Second, 5*time.Second).Should(Equal("2"))

			By("cleaning up resources")
			cmd = exec.Command("kubectl", "delete", "scaler", "out-window-scaler")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "deployment", "test-deployment-out-window")
			_, _ = utils.Run(cmd)
		})

		It("should verify metrics contain scaler controller information", func() {
			By("checking controller metrics for scaler reconciliation")
			metricsOutput := getMetricsOutput()

			Expect(metricsOutput).To(ContainSubstring("controller_runtime_reconcile_total"))
			Expect(metricsOutput).To(ContainSubstring("controller=\"scaler\""))
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
