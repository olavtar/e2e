package main

import (
	"flag"
	"fmt"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	fmt.Println("Running")
	var err error
	var config *rest.Config
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		var kubeconfig *string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
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

	apiextensions, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// Make sure the CRD exists
	_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get("dbaasplatforms.dbaas.redhat.com", v1.GetOptions{})

	if err != nil {
		fmt.Println("Error retrieving CRD", err)
	} else {
		fmt.Println("CRD found")
	}

	//Get ci-secret's data
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	ciSecret, error := clientset.CoreV1().Secrets("osde2e-ci-secrets").Get("ci-secrets", v1.GetOptions{})
	if error != nil {
		fmt.Println("Error getting ciSecret", error)
	} else {
		fmt.Println("ciSecret Found: ")
		//get the list of providers
		if providerListSecret, ok := ciSecret.Data["providerList"]; ok {
			fmt.Printf("providerListSecret = %s, ok = %v\n", providerListSecret, ok)
			var providers = strings.Split(string(providerListSecret), ",")
			fmt.Println(providers)
			for _, providerName := range providers {
				fmt.Println(providerName)
				var secretData = make(map[string]string)
				for key, value := range ciSecret.Data {
					if strings.HasPrefix(key, providerName) {
						fmt.Printf("    %s: %s\n", key, value)
						var keyName = strings.Split(key, "-")
						fmt.Println(keyName[1])
						//create map of secret data
						secretData[keyName[1]] = string(value)
					}
				}
				fmt.Println(secretData)
				//create secret
				secret := v12.Secret{
					TypeMeta: v1.TypeMeta{
						Kind:       "Secret",
						APIVersion: "v1",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "dbaas-vendor-credentials-" + string(time.Now().UnixNano()),
						Namespace: "openshift-dbaas-operator",
					},
					Data: v1.{
						secretData,
					},
				}
				if _, err := clientset.CoreV1().Secrets("openshift-dbaas-operator").Create(secret); err != nil {
					fmt.Printf("Failed to create secret for : %v", err)
				}
			}
		} else {
			fmt.Printf("providerListSecret not found\n")
		}

		//for key, value := range ciSecret.Data {
		//	fmt.Printf("    %s: %s\n", key, value)
		//	if key == "providerList" {
		//		fmt.Println("providerList: ", value)
		//	}
		//}
		//for key, value := range ciSecret.Data {
		//	// key is string, value is []byte
		//	fmt.Printf("    %s: %s\n", key, value)
		//}
	}
}

//var _ = ginkgo.Describe("DBaaS Operator Tests", func() {
//	defer ginkgo.GinkgoRecover()
//	config, err := rest.InClusterConfig()
//
//	if err != nil {
//		panic(err)
//	}
//
//	ginkgo.It("dbaasplatforms.dbaas.redhat.com CRD exists", func() {
//		apiextensions, err := clientset.NewForConfig(config)
//		Expect(err).NotTo(HaveOccurred())
//
//		// Make sure the CRD exists
//		_, err = apiextensions.ApiextensionsV1().CustomResourceDefinitions().Get("dbaasplatforms.dbaas.redhat.com", v1.GetOptions{})
//
//		if err != nil {
//			metadata.Instance.FoundCRD = false
//		} else {
//			metadata.Instance.FoundCRD = true
//		}
//
//		Expect(err).NotTo(HaveOccurred())
//	})
//})
