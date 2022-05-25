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
	routev1 "github.com/openshift/api/route/v1"
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
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

var _ = Describe("Rhoda e2e Test", func() {
	config := getConfig()
	namespace := "openshift-dbaas-operator"

	Context("Check operator installation", func() {
		apiextensions, err := apiserver.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		It("Should pass when operator installation is validated", func() {
			fmt.Println("checking operator installation")
			// Make sure the CRD exists
			_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), "dbaasplatforms.dbaas.redhat.com", meta.GetOptions{})
			if err != nil {
				AbortSuite(err.Error())
			}
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Get all the providers and loop through it to create Secrets and Inventory", func() {
		var providers []rhoda.ProviderAccount
		//Get ci-secret's data
		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		ciSecret, err := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get(context.TODO(), "ci-secrets", meta.GetOptions{})
		if err != nil {
			AbortSuite(err.Error())
		}

		//get the list of providers by getting providerList secret
		if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
			//fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
			providerNames := strings.Split(string(providerListSecret), ",")
			providers = getProvidersData(providerNames, ciSecret.Data)
		} else {
			AbortSuite("ProviderList secret was not found")
		}

		//add dbaas scheme for inventory creation
		scheme := runtime.NewScheme()
		err = dbaasv1alpha1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())

		client, err := k8sClient.New(config, k8sClient.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		//loop through providers
		for i := range providers {
			provider := providers[i]
			It("Should pass when secret and provider created and connection status is checked for "+provider.ProviderName, func() {
				DeferCleanup(func() {
					fmt.Println("DeferCleanup Started")
					fmt.Println("deleting Secret: " + provider.SecretName)
					Expect(clientset.CoreV1().Secrets("openshift-dbaas-operator").Delete(context.Background(), provider.SecretName, meta.DeleteOptions{})).Should(Succeed())

					fmt.Println("deleting Connection and Provider for: " + provider.ProviderName)

					By("deleting DBaaSConnection")
					inventory := dbaasv1alpha1.DBaaSInventory{}

					//get Inventory
					err := client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: namespace,
						Name:      "provider-acct-test-e2e-" + provider.ProviderName,
					}, &inventory)
					Expect(err).NotTo(HaveOccurred())
					if len(inventory.Status.Instances) > 0 {
						fmt.Println(inventory.Status.Instances[0].Name)

						//get inventory's first dbaas connection
						dbaaSConnection := dbaasv1alpha1.DBaaSConnection{}
						err = client.Get(context.Background(), k8sClient.ObjectKey{
							Namespace: namespace,
							Name:      inventory.Status.Instances[0].Name,
						}, &dbaaSConnection)
						Expect(err).NotTo(HaveOccurred())
						fmt.Println("deleting dbaas connection: " + inventory.Status.Instances[0].Name)
						Expect(client.Delete(context.Background(), &dbaaSConnection)).Should(Succeed())
					}

					By("deleting Provider Account")
					fmt.Println("deleting provider Acct: " + "provider-acct-test-e2e-" + provider.ProviderName)
					Expect(client.Delete(context.Background(), &inventory)).Should(Succeed())
				})

				fmt.Println("Creating secret for : " + provider.ProviderName)
				//create secret
				secret := core.Secret{
					TypeMeta: meta.TypeMeta{
						Kind:       "Secret",
						APIVersion: "v1",
					},
					ObjectMeta: meta.ObjectMeta{
						Name:      provider.SecretName,
						Namespace: namespace,
					},
					Data: provider.SecretData,
				}
				_, err = clientset.CoreV1().Secrets("openshift-dbaas-operator").Create(context.TODO(), &secret, meta.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				//create inventory
				fmt.Println("Creating inventory for : " + provider.ProviderName)
				inventory := dbaasv1alpha1.DBaaSInventory{
					TypeMeta: meta.TypeMeta{
						Kind:       "DBaaSInventory",
						APIVersion: "dbaas.redhat.com/v1alpha1",
					},
					ObjectMeta: meta.ObjectMeta{
						Name:      "provider-acct-test-e2e-" + provider.ProviderName,
						Namespace: namespace,
						Labels:    map[string]string{"related-to": "dbaas-operator", "type": "dbaas-vendor-service"},
					},
					Spec: dbaasv1alpha1.DBaaSOperatorInventorySpec{
						ProviderRef: dbaasv1alpha1.NamespacedName{
							Namespace: namespace,
							Name:      string(provider.SecretData["providerType"]),
						},
						DBaaSInventorySpec: dbaasv1alpha1.DBaaSInventorySpec{
							CredentialsRef: &dbaasv1alpha1.NamespacedName{
								Namespace: namespace,
								Name:      provider.SecretName,
							},
						},
					},
				}
				err = client.Create(context.Background(), &inventory)
				Expect(err).NotTo(HaveOccurred())

				//sleep timer to make sure things run in parallel
				//fmt.Printf("Current Unix Time: %v\n", time.Now())
				//time.Sleep(30 * time.Second)
				//fmt.Printf("Current Unix Time: %v\n", time.Now())

				//Check inventories status
				inventoryStatusCheck := dbaasv1alpha1.DBaaSInventory{}
				Eventually(func() bool {
					fmt.Println("Checking status for : " + provider.ProviderName)
					err := client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: namespace,
						Name:      "provider-acct-test-e2e-" + provider.ProviderName,
					}, &inventoryStatusCheck)
					Expect(err).NotTo(HaveOccurred())
					if len(inventoryStatusCheck.Status.Conditions) > 0 {
						for _, inventStatus := range inventoryStatusCheck.Status.Conditions {
							if inventStatus.Type == "SpecSynced" {
								return inventStatus.Status == "True"
							}
						}
						return false
					} else {
						fmt.Println("inventory.Status.Conditions Len is 0")
						return false
					}
				}, 60*time.Second, 5*time.Second).Should(BeTrue(), "Inventory Status is not Ready for connection")

				//test connection
				fmt.Println(inventoryStatusCheck.Name)
				if len(inventoryStatusCheck.Status.Instances) > 0 {
					testDBaaSConnection := dbaasv1alpha1.DBaaSConnection{
						TypeMeta: meta.TypeMeta{
							Kind:       "DBaaSConnection",
							APIVersion: "dbaas.redhat.com/v1alpha1",
						},
						ObjectMeta: meta.ObjectMeta{
							Name:      inventoryStatusCheck.Status.Instances[0].Name,
							Namespace: namespace,
						},
						Spec: dbaasv1alpha1.DBaaSConnectionSpec{
							InventoryRef: dbaasv1alpha1.NamespacedName{
								Namespace: namespace,
								Name:      inventoryStatusCheck.Name,
							},
							InstanceID: inventoryStatusCheck.Status.Instances[0].InstanceID,
						},
					}
					Expect(client.Create(context.Background(), &testDBaaSConnection)).Should(Succeed())

					By("checking DBaaSConnection status for: " + inventoryStatusCheck.Status.Instances[0].Name)
					Eventually(func() bool {
						dbaaSConnectionCheck := dbaasv1alpha1.DBaaSConnection{}
						fmt.Println("checking DBaaSConnection status for: " + inventoryStatusCheck.Status.Instances[0].Name)
						err := client.Get(context.Background(), k8sClient.ObjectKey{
							Namespace: namespace,
							Name:      inventoryStatusCheck.Status.Instances[0].Name,
						}, &dbaaSConnectionCheck)
						Expect(err).NotTo(HaveOccurred())
						if len(dbaaSConnectionCheck.Status.Conditions) > 0 {
							for _, connStatus := range dbaaSConnectionCheck.Status.Conditions {
								if connStatus.Type == "ReadyForBinding" {
									return connStatus.Status == "True"
								}
							}
							return false
						} else {
							fmt.Println("dbaaSConnection.Status.Conditions Len is 0")
							return false
						}
					}, 60*time.Second, 5*time.Second).Should(BeTrue())
				} else {
					fmt.Println("No instances to connect")
				}
			})
		}
	})

	It("Console Login UI and pages access check", func() {
		fmt.Println("Login")
		ctx, cancel := chromedp.NewContext(context.Background())
		defer cancel()
		//Setting timeout
		//ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		//defer cancel()

		//Adding route to the scheme to get the domain
		scheme := runtime.NewScheme()
		routev1.Install(scheme)
		err := dbaasv1alpha1.AddToScheme(scheme)
		Expect(err).NotTo(HaveOccurred())
		client, err := k8sClient.New(config, k8sClient.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())
		route := routev1.Route{}
		err = client.Get(context.Background(), k8sClient.ObjectKey{
			Namespace: "openshift-console",
			Name:      "console",
		}, &route)
		Expect(err).NotTo(HaveOccurred())
		fmt.Println(route.Spec.Host)
		domain := route.Spec.Host

		var nodesButtonList []*cdp.Node
		//selector for checking the Data Services button
		selector := "#page-sidebar div ul li button"
		url := fmt.Sprintf("https://%s/dashboards", domain)
		//	url := "https://console-openshift-console.apps.rhoda-lab.51ty.p1.openshiftapps.com/dashboards"

		if err := chromedp.Run(ctx,
			SetOpenShiftCookie(config.BearerToken, domain),
			chromedp.Navigate(url),
			chromedp.WaitVisible(`#page-sidebar`),
			chromedp.Nodes(selector, &nodesButtonList),
		); err != nil {
			AbortSuite(err.Error())
		}

		selectorLi := "section ul li a"
		var dataServiceNode []*cdp.Node
		//Get Data Services button
		dataServiceLi := getLi(nodesButtonList)
		err = chromedp.Run(ctx,
			chromedp.Nodes(selectorLi, &dataServiceNode, chromedp.ByQueryAll, chromedp.FromNode(dataServiceLi)),
		)
		Expect(err).NotTo(HaveOccurred())

		href := getHref(dataServiceNode)
		fmt.Println(href)
		u := fmt.Sprintf("https://%s%s", domain, href)
		fmt.Printf(u)
		dataAccessSelector := "#content-scrollable h1 div span"
		var dataAccessNodes []*cdp.Node
		err = chromedp.Run(ctx,
			chromedp.Navigate(u),
			chromedp.WaitVisible(`#content-scrollable button`),
			chromedp.Nodes(dataAccessSelector, &dataAccessNodes, chromedp.ByQuery),
			//chromedp.EvaluateAsDevTools(`document.querySelector("#content-scrollable h1 div" ).innerHTML.includes("Database Access")`, &textExists),
		)
		Expect(err).NotTo(HaveOccurred())
		for _, daNode := range dataAccessNodes {
			//look under h1/div that the text is equal to "Database Access", means the page loaded, otherwise page loaded with errors.
			text := daNode.Parent.Children[0].NodeValue
			fmt.Println(text)
			Expect(text).Should(Equal("Database Access"))
		}
	})
})

func getHref(dataServiceNode []*cdp.Node) string {
	for _, aNode := range dataServiceNode {
		text := aNode.Children[0].NodeValue
		fmt.Println(text)
		if aNode.Children[0].NodeValue == "Database Access" {
			return aNode.AttributeValue("href")
		}
	}
	return ""
}

func getLi(nodesButtonList []*cdp.Node) *cdp.Node {
	for _, node := range nodesButtonList {
		//NodeName is the button here, looping through buttons to get Data services
		fmt.Println(node.NodeName)

		for _, child := range node.Children {
			//getting Data Services Button
			fmt.Println(child.NodeValue)
			if child.NodeValue == "Data Services" {
				//get the parent's parent which is Li to click on the Database Access button
				return child.Parent.Parent
			}
		}
	}
	return nil
}

func getProvidersData(providerNames []string, ciSecretData map[string][]byte) []rhoda.ProviderAccount {
	var providers []rhoda.ProviderAccount
	for _, providerName := range providerNames {
		fmt.Println(providerName)
		var secretData = make(map[string][]byte)
		for key, value := range ciSecretData {
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
	return providers
}

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
		fmt.Println(config)
		fmt.Println(config.Host)

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

func SetOpenShiftCookie(value, domain string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
		success := network.SetCookie("openshift-session-token", value).
			WithExpires(&expr).
			WithDomain(domain).
			WithPath("/").
			WithHTTPOnly(false).
			WithSecure(false).
			Do(ctx)
		if success != nil {
			return fmt.Errorf("could not set cookie openshift-session-token")
		}
		return nil
	})
}
