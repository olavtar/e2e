package rhoda_test

import (
	"context"
	"crypto/tls"
	"e2e/pkg/rhoda"
	"flag"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	dbaasv1alpha1 "github.com/RHEcosystemAppEng/dbaas-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/net/html"
	"io/ioutil"
	core "k8s.io/api/core/v1"
	apiserver "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"net/http"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

var _ = Describe("Rhoda e2e Test", func() {
	var config *rest.Config
	namespace := "openshift-dbaas-operator"

	Context("Check operator installation", func() {
		fmt.Println("Running 1st Context")
		config = getConfig()

		apiextensions, err := apiserver.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())

		// Make sure the CRD exists
		_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "dbaasplatforms.dbaas.redhat.com", meta.GetOptions{})
		if err != nil {
			panic(err.Error())
		}
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Get all the providers and loop through it to create Secrets and Inventory", func() {
		var providerNames []string
		var providers []rhoda.ProviderAccount

		//Get ci-secret's data
		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())

		ciSecret, err := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get(context.TODO(), "ci-secrets", meta.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		fmt.Println("ciSecret Found: ")
		//get the list of providers by getting providerList secret
		if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
			fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
			providerNames = strings.Split(string(providerListSecret), ",")
			fmt.Println(providerNames)
		} else {
			AbortSuite("ProviderList secret was not found")
		}
		//populate providers array
		for _, providerName := range providerNames {
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
			//add to array
			providers = append(providers, rhoda.ProviderAccount{ProviderName: providerName, SecretName: "dbaas-secret-e2e-" + providerName, SecretData: secretData})
		}

		//add dbaas scheme for inventory creation
		scheme := runtime.NewScheme()
		err = dbaasv1alpha1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		c, err := client.New(config, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		//loop through providers
		for i := range providers {
			value := providers[i]
			It("Should pass when secret is created for "+value.ProviderName, func() {
				fmt.Println("Creating secret for : " + value.ProviderName)
				//create secret
				secret := core.Secret{
					TypeMeta: meta.TypeMeta{
						Kind:       "Secret",
						APIVersion: "v1",
					},
					ObjectMeta: meta.ObjectMeta{
						Name:      value.SecretName,
						Namespace: namespace,
					},
					Data: value.SecretData,
				}
				_, err = clientset.CoreV1().Secrets("openshift-dbaas-operator").Create(context.TODO(), &secret, meta.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
			})
			//create inventory
			It("Should pass when inventory is created for "+value.ProviderName, func() {
				fmt.Println("Creating inventory for : " + value.ProviderName)
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
								Name:      value.SecretName,
							},
						},
					},
				}
				err = c.Create(context.Background(), &inventory)
				Expect(err).NotTo(HaveOccurred())
			})

			It("Should pass when inventory is processed for "+value.ProviderName, func() {
				fmt.Println("Checking status for : " + value.ProviderName)
				//fmt.Printf("Current Unix Time: %v\n", time.Now())
				//time.Sleep(30 * time.Second)
				//fmt.Printf("Current Unix Time: %v\n", time.Now())
				//Check inventories status
				inventory := dbaasv1alpha1.DBaaSInventory{}
				Eventually(func() bool {
					fmt.Println("Eventually loop for : " + value.ProviderName)
					err := c.Get(context.Background(), client.ObjectKey{
						Namespace: namespace,
						Name:      "provider-acct-test-e2e-" + value.ProviderName,
					}, &inventory)
					Expect(err).NotTo(HaveOccurred())
					if len(inventory.Status.Conditions) > 0 {
						return inventory.Status.Conditions[0].Status == "True"
					} else {
						fmt.Println("inventory.Status.Conditions Len is 0")
						return false
					}
				}, 60*time.Second, 5*time.Second).Should(BeTrue())

				//test connection
				if inventory.Status.Conditions[0].Status == "True" {
					fmt.Println(inventory.Name)
					if len(inventory.Status.Instances) > 0 {
						fmt.Println(inventory.Status.Instances[0].InstanceID)
						fmt.Println(inventory.Status.Instances[0].Name)

						testDBaaSConnection := dbaasv1alpha1.DBaaSConnection{
							TypeMeta: meta.TypeMeta{
								Kind:       "DBaaSConnection",
								APIVersion: "dbaas.redhat.com/v1alpha1",
							},
							ObjectMeta: meta.ObjectMeta{
								Name:      inventory.Status.Instances[0].Name,
								Namespace: namespace,
							},
							Spec: dbaasv1alpha1.DBaaSConnectionSpec{
								InventoryRef: dbaasv1alpha1.NamespacedName{
									Namespace: namespace,
									Name:      inventory.Name,
								},
								InstanceID: inventory.Status.Instances[0].InstanceID,
							},
						}

						testDBaaSConnection.SetResourceVersion("")
						Expect(c.Create(context.Background(), &testDBaaSConnection)).Should(Succeed())
						By("checking DBaaSConnection status for: " + inventory.Status.Instances[0].Name)
						Eventually(func() bool {
							err := c.Get(context.Background(), client.ObjectKey{
								Namespace: namespace,
								Name:      inventory.Status.Instances[0].Name,
							}, &testDBaaSConnection)
							Expect(err).NotTo(HaveOccurred())
							fmt.Println("checking DBaaSConnection status for: " + inventory.Status.Instances[0].Name)
							if len(testDBaaSConnection.Status.Conditions) > 0 {
								return testDBaaSConnection.Status.Conditions[0].Status == "True"
							} else {
								fmt.Println("testDBaaSConnection.Status.Conditions Len is 0")
								return false
							}
						}, 60*time.Second, 5*time.Second).Should(BeTrue())
					} else {
						fmt.Println("No instances to connect")
					}
				} else {
					fmt.Println("Inventory Status is not Ready for connection")
					Expect(inventory.Status.Conditions[0].Status).To(Equal("True"))
				}
			})
		}
	})
	Context("UI Check", func() {
		It("UI Check", func() {
			fmt.Println("Running UI Check")
			//	url := "https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com/"
			//url := "https://www.google.com/"
			//
			//req, err := http.NewRequest("GET", url, nil)
			//if err != nil {
			//	panic(err.Error())
			//}
			//c := &http.Client{
			//	Timeout: 5 * time.Second,
			//}
			//resp, err := c.Do(req)
			//if err != nil {
			//	panic(err.Error())
			//}
			//defer resp.Body.Close()

			// Generated by curl-to-Go: https://mholt.github.io/curl-to-go

			// curl -k \
			//     -H "Authorization: Bearer $TOKEN" \
			//     -H 'Accept: application/json' \
			//     https://$ENDPOINT/oapi/v1/projects

			// TODO: This is insecure; use only in dev environments.
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client := &http.Client{Transport: tr}

			req, err := http.NewRequest("GET", os.ExpandEnv("https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com/k8s/ns/openshift-dbaas-operator/rhoda-admin-dashboard"), nil)
			if err != nil {
				// handle err
			}
			req.Header.Set("Authorization", os.ExpandEnv("Bearer sha256~RP9ksn-Rg4aZxPGRgvESgCvhPJJEuLBmc7UsuCl8IHI"))
			req.Header.Set("Accept", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				// handle err
			}
			defer resp.Body.Close()

			code := resp.StatusCode
			fmt.Println("Code: ", code)
			fmt.Println(http.StatusOK)
			if resp.StatusCode != 200 {
				Fail("failed to fetch data: %d %s", resp.StatusCode)
			}
			body, err := ioutil.ReadAll(resp.Body)
			fmt.Println(string(body))
			node, _ := html.Parse(resp.Body)
			document := goquery.NewDocumentFromNode(node)
			s := document.Find("div#app")
			fmt.Println(s)
			//load html doc
			//doc, err := goquery.NewDocumentFromReader(resp.Body)
			////	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
			//if err != nil {
			//	panic(err.Error())
			//}
			//doc.Find("*").Each(func(_ int, node *goquery.Selection) {
			//	println(node.Text())
			//	})
			//doc.Find(".NKcBbd").Each(func(i int, s *goquery.Selection) {
			//	// For each item found, get the band and title
			//	fmt.Println("next")
			//	class, _ := s.Attr("class")
			//	fmt.Println(class, s.Text())
			//})
			//Expect(string(body)).Should(ContainSubstring("Red Hat OpenShift Dedicated"))
			Expect(err).NotTo(HaveOccurred())
			//if err == nil && code != http.StatusOK {
			//	return nil, fmt.Errorf(string(body))
			//}
			//if code != http.StatusOK {
			//	return nil, fmt.Errorf("Server status error: %v", http.StatusText(code))
			//}
		})
	})
})

func getConfig() *rest.Config {
	fmt.Println("Running getConfig")
	var config *rest.Config
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
	if err != nil {
		panic(err.Error())
	}
	return config
}
