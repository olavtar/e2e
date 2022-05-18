package rhoda_test

import (
	"context"
	"e2e/pkg/rhoda"
	"flag"
	"fmt"
	dbaasv1alpha1 "github.com/RHEcosystemAppEng/dbaas-operator/api/v1alpha1"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
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

	Context("Check operator installation", func() {
		config = getConfig()
		apiextensions, err := apiserver.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		It("Should pass when operator installation is validated", func() {
			fmt.Println("checking operator installation")
			// Make sure the CRD exists
			_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "dbaasplatforms.dbaas.redhat.com", meta.GetOptions{})
			if err != nil {
				panic(err.Error())
			}
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Get all the providers and loop through it to create Secrets and Inventory", func() {
		var providerNames []string
		var providers []rhoda.ProviderAccount

		//Get ci-secret's data
		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())

		ciSecret, err := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get(context.TODO(), "ci-secrets", meta.GetOptions{})
		if err != nil {
			AbortSuite(err.Error())
		}
		fmt.Println("ciSecret Found: ")
		//get the list of providers by getting providerList secret
		if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
			//fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
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
			//add provider's data to an array
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
				//fmt.Printf("Current Unix Time: %v\n", time.Now())
				//time.Sleep(30 * time.Second)
				//fmt.Printf("Current Unix Time: %v\n", time.Now())
				//Check inventories status
				inventory := dbaasv1alpha1.DBaaSInventory{}
				Eventually(func() bool {
					fmt.Println("Checking status for : " + value.ProviderName)
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
							fmt.Println("checking DBaaSConnection status for: " + inventory.Status.Instances[0].Name)
							err := c.Get(context.Background(), client.ObjectKey{
								Namespace: namespace,
								Name:      inventory.Status.Instances[0].Name,
							}, &testDBaaSConnection)
							Expect(err).NotTo(HaveOccurred())
							if len(testDBaaSConnection.Status.Conditions) > 0 {
								return testDBaaSConnection.Status.Conditions[0].Status == "True"
							} else {
								fmt.Println("testDBaaSConnection.Status.Conditions Len is 0")
								return false
							}
						}, 60*time.Second, 5*time.Second).Should(BeTrue())

						//Clean up of the cluster
						//deleting connection(instance)
						//By("deleting DBaaSConnection")
						//fmt.Println("deleting dbaas connection: " + inventory.Status.Instances[0].Name)
						//Expect(c.Delete(context.Background(), &testDBaaSConnection)).Should(Succeed())
						//
						//By("checking DBaaSConnection deleted")
						//Eventually(func() bool {
						//	err := c.Get(context.Background(), client.ObjectKeyFromObject(&testDBaaSConnection), &dbaasv1alpha1.DBaaSConnection{})
						//	if err != nil && errors.IsNotFound(err) {
						//		fmt.Println("Deleted, no connection found")
						//		return true
						//	}
						//	return false
						//}, 60*time.Second, 5*time.Second).Should(BeTrue())

					} else {
						fmt.Println("No instances to connect")
					}
				} else {
					fmt.Println("Inventory Status is not Ready for connection")
					Expect(inventory.Status.Conditions[0].Status).To(Equal("True"))
				}

				//deleting Provider Account(Inventory)
				//fmt.Println("deleting provider Acct: " + "provider-acct-test-e2e-" + value.ProviderName)
				//By("deleting Provider Account")
				//Expect(c.Delete(context.Background(), &inventory)).Should(Succeed())
				//
				//By("checking Provider Acct deleted")
				//Eventually(func() bool {
				//	err := c.Get(context.Background(), client.ObjectKeyFromObject(&inventory), &dbaasv1alpha1.DBaaSInventory{})
				//	if err != nil && errors.IsNotFound(err) {
				//		fmt.Println("Deleted, no provider acct found")
				//		return true
				//	}
				//	return false
				//}, 60*time.Second, 5*time.Second).Should(BeTrue())
				//
				////deleting secret
				//By("Deleting Secret")
				//fmt.Println("deleting Secret: " + value.SecretName)
				//Expect(clientset.CoreV1().Secrets("openshift-dbaas-operator").Delete(context.Background(), value.SecretName, meta.DeleteOptions{})).Should(Succeed())
				//
				//By("checking Secret deleted")
				//Eventually(func() bool {
				//	_, err := clientset.CoreV1().Secrets("openshift-dbaas-operator").Get(context.Background(), value.SecretName, meta.GetOptions{})
				//	if err != nil && errors.IsNotFound(err) {
				//		fmt.Println("Deleted, no secret found")
				//		return true
				//	}
				//	return false
				//}, 60*time.Second, 5*time.Second).Should(BeTrue())
			})
		}

		//Describe("After All Clean up cluster", func() {
		//	for i := range providers {
		//		value := providers[i]
		//		It("Cleaning up Secrets, Provider Acct and Dbaas Connections", func() {
		//			fmt.Println("deleting Secret: " + value.SecretName)
		//			Expect(clientset.CoreV1().Secrets("openshift-dbaas-operator").Delete(context.Background(), value.SecretName, meta.DeleteOptions{})).Should(Succeed())
		//
		//			By("checking Secret deleted")
		//			Eventually(func() bool {
		//				_, err := clientset.CoreV1().Secrets("openshift-dbaas-operator").Get(context.Background(), value.SecretName, meta.GetOptions{})
		//				if err != nil && errors.IsNotFound(err) {
		//					fmt.Println("Deleted, no secret found")
		//					return true
		//				}
		//				return false
		//			}, 60*time.Second, 5*time.Second).Should(BeTrue())
		//
		//			//})
		//
		//			//	It("Cleaning up Providers and instances", func() {
		//			fmt.Println("deleting Connection and Provider for: " + value.ProviderName)
		//
		//			By("deleting DBaaSConnection")
		//			inventory := dbaasv1alpha1.DBaaSInventory{}
		//
		//			//get Inventory
		//			err := c.Get(context.Background(), client.ObjectKey{
		//				Namespace: namespace,
		//				Name:      "provider-acct-test-e2e-" + value.ProviderName,
		//			}, &inventory)
		//			Expect(err).NotTo(HaveOccurred())
		//			if len(inventory.Status.Instances) > 0 {
		//				fmt.Println(inventory.Status.Instances[0].Name)
		//
		//				//get inventory's first dbaas connection
		//				dbaaSConnection := dbaasv1alpha1.DBaaSConnection{}
		//				err = c.Get(context.Background(), client.ObjectKey{
		//					Namespace: namespace,
		//					Name:      inventory.Status.Instances[0].Name,
		//				}, &dbaaSConnection)
		//				Expect(err).NotTo(HaveOccurred())
		//				fmt.Println("deleting dbaas connection: " + inventory.Status.Instances[0].Name)
		//				Expect(c.Delete(context.Background(), &dbaaSConnection)).Should(Succeed())
		//
		//				By("checking DBaaSConnection deleted")
		//				Eventually(func() bool {
		//					err := c.Get(context.Background(), client.ObjectKeyFromObject(&dbaaSConnection), &dbaasv1alpha1.DBaaSConnection{})
		//					if err != nil && errors.IsNotFound(err) {
		//						fmt.Println("Deleted, no connection found")
		//
		//						return true
		//					}
		//					return false
		//				}, 60*time.Second, 5*time.Second).Should(BeTrue())
		//			}
		//			By("deleting Provider Account")
		//			fmt.Println("deleting provider Acct: " + "provider-acct-test-e2e-" + value.ProviderName)
		//			Expect(c.Delete(context.Background(), &inventory)).Should(Succeed())
		//
		//			By("checking Provider Acct deleted")
		//			Eventually(func() bool {
		//				err := c.Get(context.Background(), client.ObjectKeyFromObject(&inventory), &dbaasv1alpha1.DBaaSInventory{})
		//				if err != nil && errors.IsNotFound(err) {
		//					fmt.Println("Deleted, no provider acct found")
		//					return true
		//				}
		//				return false
		//			}, 60*time.Second, 5*time.Second).Should(BeTrue())
		//
		//		})
		//	}
		//})
	})

	It("Console Login UI ", func() {
		fmt.Println("Login")
		ctx, cancel := chromedp.NewContext(context.Background())
		defer cancel()
		//Setting timeout
		//ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		//defer cancel()

		var nodes []*cdp.Node
		//selector for checking the Data Services button
		selector := "#page-sidebar div ul li button"
		url := "https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com/dashboards"
		// url:= "https://console-openshift-console.apps.rhoda-sp-prod.lue0.p1.openshiftapps.com/dashboards"
		var data string

		if err := chromedp.Run(ctx,
			SetCookie("openshift-session-token", config.BearerToken, "console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com", "/", false, false),
			chromedp.Navigate(url),
			chromedp.WaitVisible(`#page-sidebar`),
			chromedp.OuterHTML("html", &data, chromedp.ByQuery),
			//	chromedp.Nodes(`button`, &nodes, chromedp.ByQueryAll),

			chromedp.Nodes(selector, &nodes),
			//chromedp.Text(`button`, &selector, chromedp.NodeVisible, chromedp.ByQuery),
		); err != nil {
			//panic(err)
			AbortSuite(err.Error())
		}

		for _, node := range nodes {
			//NodeName is the button here, looping through buttons to get Data services
			fmt.Println(node.NodeName)

			for _, child := range node.Children {
				//getting Data Services Button
				fmt.Println(child.NodeValue)
				if child.NodeValue == "Data Services" {
					//get the parent's parent which is Li to click on the Database Access button
					li := child.Parent.Parent
					//fmt.Println(li)
					//checkAdminDashboard(li, ctx)
					var data string
					selector := "section ul li a"
					var result []*cdp.Node

					err := chromedp.Run(ctx,
						chromedp.Nodes(selector, &result, chromedp.ByQuery, chromedp.FromNode(li)),
					)
					Expect(err).NotTo(HaveOccurred())

					//fmt.Println(result)
					for _, aNode := range result {
						//temp stuff for visibility
						text := aNode.Children[0].NodeValue
						fmt.Println(text)
						//	u := aNode.AttributeValue("href")
						//	fmt.Printf("node: %s | href = %s\n", aNode.LocalName, u)
						var textExists bool

						if aNode.Children[0].NodeValue == "Database Access" {
							u := "https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com" + aNode.AttributeValue("href")
							fmt.Printf("node: %s | href = %s\n", aNode.LocalName, u)
							_, err := chromedp.RunResponse(ctx,
								chromedp.Navigate(u),
								chromedp.WaitVisible(`#content-scrollable button`),
								chromedp.OuterHTML("html", &data, chromedp.ByQuery),
								chromedp.EvaluateAsDevTools(`document.querySelector("#content-scrollable h1 div" ).innerHTML.includes("Database Access")`, &textExists),
							)
							Expect(err).NotTo(HaveOccurred())
							Expect(textExists).Should(BeTrue())
						}
					}
				}
			}
		}
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
		fmt.Println("Config:")
		token := config.BearerToken
		fmt.Println(token)

	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		panic(err.Error())
	}
	return config
}

//func checkAdminDashboard(li *cdp.Node, ctx context.Context) {
//	fmt.Println("checkAdminDashboard")
//	fmt.Println(li)
//	var data string
//	selector := "section ul li a"
//	var result []*cdp.Node
//
//	if err := chromedp.Run(ctx,
//		chromedp.Nodes(selector, &result, chromedp.ByQuery, chromedp.FromNode(li)),
//	); err != nil {
//		panic(err)
//	}
//	fmt.Println(result)
//	for _, aNode := range result {
//		//temp stuff for visibility
//		text := aNode.Children[0].NodeValue
//		fmt.Println(text)
//		u := aNode.AttributeValue("href")
//		fmt.Printf("node: %s | href = %s\n", aNode.LocalName, u)
//		textSelector := "#content-scrollable h2"
//		var dataAccessResult []*cdp.Node
//
//		if aNode.Children[0].NodeValue == "Database Access" {
//			u := "https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com" + aNode.AttributeValue("href")
//			fmt.Printf("node: %s | href = %s\n", aNode.LocalName, u)
//			resp, err := chromedp.RunResponse(ctx,
//				chromedp.Navigate(u),
//				chromedp.WaitVisible(`#content-scrollable button`),
//				chromedp.OuterHTML("html", &data, chromedp.ByQuery),
//				chromedp.Nodes(textSelector, &dataAccessResult),
//			)
//			if err != nil {
//				fmt.Println("Error", err.Error())
//				panic(err.Error())
//			}
//			status2 := resp.Status
//			fmt.Println("second status code:", status2)
//			for _, dataAccessNode := range dataAccessResult {
//				fmt.Println(dataAccessNode.Children[0].NodeValue)
//			}
//		}
//	}
//}

func SetCookie(name, value, domain, path string, httpOnly, secure bool) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
		success := network.SetCookie(name, value).
			WithExpires(&expr).
			WithDomain(domain).
			WithPath(path).
			WithHTTPOnly(httpOnly).
			WithSecure(secure).
			Do(ctx)
		if success != nil {
			return fmt.Errorf("could not set cookie %s", name)
		}
		return nil
	})
}

//func cleanUpCluster() {
//	fmt.Println("Running CleanUp")
//	clientset, err := kubernetes.NewForConfig(config)
//	Expect(err).NotTo(HaveOccurred())
//	scheme := runtime.NewScheme()
//	err = dbaasv1alpha1.AddToScheme(scheme)
//	Expect(err).NotTo(HaveOccurred())
//
//	c, err := client.New(config, client.Options{Scheme: scheme})
//	Expect(err).NotTo(HaveOccurred())
//
//	for i := range providers {
//		value := providers[i]
//
//		fmt.Println("deleting Secret: " + value.SecretName)
//		Expect(clientset.CoreV1().Secrets("openshift-dbaas-operator").Delete(context.Background(), value.SecretName, meta.DeleteOptions{})).Should(Succeed())
//
//		By("checking Secret deleted")
//		Eventually(func() bool {
//			_, err := clientset.CoreV1().Secrets("openshift-dbaas-operator").Get(context.Background(), value.SecretName, meta.GetOptions{})
//			if err != nil && errors.IsNotFound(err) {
//				fmt.Println("Deleted, no secret found")
//				return true
//			}
//			return false
//		}, 60*time.Second, 5*time.Second).Should(BeTrue())
//
//		//})
//
//		//	It("Cleaning up Providers and instances", func() {
//		fmt.Println("deleting Connection and Provider for: " + value.ProviderName)
//
//		By("deleting DBaaSConnection")
//		inventory := dbaasv1alpha1.DBaaSInventory{}
//
//		//get Inventory
//		err := c.Get(context.Background(), client.ObjectKey{
//			Namespace: namespace,
//			Name:      "provider-acct-test-e2e-" + value.ProviderName,
//		}, &inventory)
//		Expect(err).NotTo(HaveOccurred())
//		if len(inventory.Status.Instances) > 0 {
//			fmt.Println(inventory.Status.Instances[0].Name)
//
//			//get inventory's first dbaas connection
//			dbaaSConnection := dbaasv1alpha1.DBaaSConnection{}
//			err = c.Get(context.Background(), client.ObjectKey{
//				Namespace: namespace,
//				Name:      inventory.Status.Instances[0].Name,
//			}, &dbaaSConnection)
//			Expect(err).NotTo(HaveOccurred())
//			fmt.Println("deleting dbaas connection: " + inventory.Status.Instances[0].Name)
//			Expect(c.Delete(context.Background(), &dbaaSConnection)).Should(Succeed())
//
//			By("checking DBaaSConnection deleted")
//			Eventually(func() bool {
//				err := c.Get(context.Background(), client.ObjectKeyFromObject(&dbaaSConnection), &dbaasv1alpha1.DBaaSConnection{})
//				if err != nil && errors.IsNotFound(err) {
//					fmt.Println("Deleted, no connection found")
//
//					return true
//				}
//				return false
//			}, 60*time.Second, 5*time.Second).Should(BeTrue())
//		}
//		By("deleting Provider Account")
//		fmt.Println("deleting provider Acct: " + "provider-acct-test-e2e-" + value.ProviderName)
//		Expect(c.Delete(context.Background(), &inventory)).Should(Succeed())
//
//		By("checking Provider Acct deleted")
//		Eventually(func() bool {
//			err := c.Get(context.Background(), client.ObjectKeyFromObject(&inventory), &dbaasv1alpha1.DBaaSInventory{})
//			if err != nil && errors.IsNotFound(err) {
//				fmt.Println("Deleted, no provider acct found")
//				return true
//			}
//			return false
//		}, 60*time.Second, 5*time.Second).Should(BeTrue())
//	}
//}
