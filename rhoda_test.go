package main_test

import (
	"context"
	"encoding/json"
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

	Context("extract secrets from the vault", func() {
		It("Get ci-secrets", func() {
			//Get ci-secret's data
			clientset, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			scheme := runtime.NewScheme()
			if err := dbaasv1alpha1.AddToScheme(scheme); err != nil {
				//fmt.Printf("Failed to create schema.", err)
			}
			c, err := client.New(config, client.Options{Scheme: scheme})
			ciSecret, err := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get(context.TODO(), "ci-secrets", meta.GetOptions{})
			if err != nil {
				fmt.Println("Error getting ciSecret", err)
			} else {
				fmt.Println("ciSecret Found: ")
				namespace := "openshift-dbaas-operator"
				//get the list of providers by getting providerList secret
				if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
					fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
					var providers = strings.Split(string(providerListSecret), ",")
					fmt.Println(providers)
					//loop through providers to create secrets and inventories
					for _, providerName := range providers {
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
								Name:      "dbaas-vendor-credentials-e2e-" + providerName,
								Namespace: namespace,
							},
							Data: secretData,
						}
						if _, err = clientset.CoreV1().Secrets("openshift-dbaas-operator").Create(context.TODO(), &secret, meta.CreateOptions{}); err != nil {
							fmt.Printf("Failed to create secret for : %v\n", err)
						}

						//create inventory
						inventory := dbaasv1alpha1.DBaaSInventory{
							TypeMeta: meta.TypeMeta{
								Kind:       "DBaaSInventory",
								APIVersion: "dbaas.redhat.com/v1alpha1",
							},
							ObjectMeta: meta.ObjectMeta{
								Name:      "provider-acct-test-e2e-" + providerName,
								Namespace: namespace,
								Labels:    map[string]string{"related-to": "dbaas-operator", "type": "dbaas-vendor-service"},
							},
							Spec: dbaasv1alpha1.DBaaSOperatorInventorySpec{
								ProviderRef: dbaasv1alpha1.NamespacedName{
									Namespace: namespace,
									Name:      string(secretData["providerType"]),
								},
								DBaaSInventorySpec: dbaasv1alpha1.DBaaSInventorySpec{
									CredentialsRef: &dbaasv1alpha1.NamespacedName{
										Namespace: namespace,
										Name:      "dbaas-vendor-credentials-e2e-" + providerName,
									},
								},
							},
						}
						if err = c.Create(context.Background(), &inventory); err != nil {
							fmt.Printf("Failed to create invenotry for : %v\n", err)
						}
						fmt.Println("Get Inventory")
						fmt.Println(time.Now())
						time.Sleep(5 * time.Second)
						fmt.Println(time.Now())
						err := c.Get(context.TODO(), client.ObjectKey{
							Namespace: namespace,
							Name:      inventory.ObjectMeta.Name,
						}, &inventory)
						if err != nil {
							panic(err.Error())
						}
						fmt.Println("inventory")
						b, err := json.MarshalIndent(inventory, "", "  ")
						if err != nil {
							fmt.Println("error:", err)
						}
						fmt.Print(string(b))

					}
				} else {
					fmt.Printf("providerListSecret not found\n")
				}
			}
		})
	})
})
