package rhoda_test

import (
	"context"
	"e2e/pkg/rhoda"
	"flag"
	"fmt"
	dbaasv1alpha1 "github.com/RHEcosystemAppEng/dbaas-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	apiserver "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

var _ = Describe("Rhoda e2e Test", func() {
	var config *rest.Config
	namespace := "openshift-dbaas-operator"
	var providers []rhoda.ProviderAccount
	var c client.Client

	Context("Check operator installation", func() {
		It("dbaasplatforms.dbaas.redhat.com CRD exists", func() {
			//running it locally, assuming we are outside the cluster
			var err error
			if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
				var kubeconfig *string
				if home := homedir.HomeDir(); home != "" {
					kubeconfig = flag.String("kconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
				} else {
					kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
				}
				flag.Parse()

				// use the current context in kubeconfig
				config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
			} else {
				config, err = rest.InClusterConfig()
			}
			Expect(err).NotTo(HaveOccurred())

			apiextensions, err := apiserver.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			// Make sure the CRD exists
			_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "dbaasplatforms.dbaas.redhat.com", meta.GetOptions{})

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Populate providers array, create secret", func() {
		//It("Get list of providers from the vault, create secrets, populate array", func() {
		//When("Getting the providers list from the ci-secrets", func() {
		//Get ci-secret's data

		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())

		ciSecret, err := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get(context.TODO(), "ci-secrets", meta.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		fmt.Println("ciSecret Found: ")
		//get the list of providers by getting providerList secret
		var providerNames []string
		if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
			fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
			providerNames = strings.Split(string(providerListSecret), ",")
			fmt.Println(providerNames)
		}

		//loop through providers to create secrets
		for _, providerName := range providerNames {
			When("something nested secret "+providerName, func() {
				It("create SecretData and a secret", func() {
					fmt.Println("Creating secret")
					fmt.Println(providerName)
					var secretData = make(map[string][]byte)
					for key, value := range ciSecret.Data {
						if strings.HasPrefix(key, providerName) {
							fmt.Printf("    %s: %s\n", key, value)
							var keyName = strings.Split(key, "-")
							//create map of secret data
							secretData[keyName[1]] = value
						}
					}

					//create secret
					secret := core.Secret{
						TypeMeta: meta.TypeMeta{
							Kind:       "Secret",
							APIVersion: "v1",
						},
						ObjectMeta: meta.ObjectMeta{
							Name:      "dbaas-secret-e2e-" + providerName,
							Namespace: namespace,
						},
						Data: secretData,
					}
					_, err = clientset.CoreV1().Secrets("openshift-dbaas-operator").Create(context.TODO(), &secret, meta.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					//add to array
					providers = append(providers, rhoda.ProviderAccount{ProviderName: providerName, SecretName: "dbaas-secret-e2e-" + providerName, SecretData: secretData})
				})
			})
		}

		//})
	})

	Describe("Create Inventory", func() {
		scheme := runtime.NewScheme()
		err := dbaasv1alpha1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		c, err = client.New(config, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		for _, value := range providers {
			When("something nested inventory "+value.ProviderName, func() {
				It("Creating inventory", func() {
					fmt.Println("Creating inventory")
					fmt.Println(value.ProviderName)
					fmt.Println(value.SecretName)
					//for k, v := range value.SecretData {
					//	fmt.Printf("    %s: %s\n", k, v)
					//}

					//create inventory
					inventory := dbaasv1alpha1.DBaaSInventory{
						TypeMeta: meta.TypeMeta{
							Kind:       "DBaaSInventory",
							APIVersion: "dbaas.redhat.com/v1alpha1",
						},
						ObjectMeta: meta.ObjectMeta{
							Name:      "provider-acct-test-e2e-" + value.ProviderName,
							Namespace: namespace,
							Labels:    map[string]string{"related-to": "dbaas-operator", "type": "dbaas-vendor-service"},
						},
						Spec: dbaasv1alpha1.DBaaSOperatorInventorySpec{
							ProviderRef: dbaasv1alpha1.NamespacedName{
								Namespace: namespace,
								Name:      string(value.SecretData["providerType"]),
							},
							DBaaSInventorySpec: dbaasv1alpha1.DBaaSInventorySpec{
								CredentialsRef: &dbaasv1alpha1.NamespacedName{
									Namespace: namespace,
									Name:      "dbaas-secret-e2e-" + value.ProviderName,
								},
							},
						},
					}
					err = c.Create(context.Background(), &inventory)
					Expect(err).NotTo(HaveOccurred())
				})
			})
		}
	})

	Context("Check inventories status", func() {
		It("Should pass when the inventory is processed", func() {
			for _, value := range providers {
				inventory := dbaasv1alpha1.DBaaSInventory{}
				Eventually(func() bool {
					fmt.Println("Checking inventory status for: " + value.ProviderName)
					err := c.Get(context.TODO(), client.ObjectKey{
						Namespace: namespace,
						Name:      "provider-acct-test-e2e-" + value.ProviderName,
					}, &inventory)
					Expect(err).NotTo(HaveOccurred())
					return inventory.Status.Conditions[0].Status == "True"
				}, 60*time.Second, 5*time.Second).Should(BeTrue())
			}
		})
	})
})
