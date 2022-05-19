package rhoda_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//var providers []rhoda.ProviderAccount
//var config *rest.Config
//var namespace = "openshift-dbaas-operator"

func TestRhoda(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rhoda Suite")
}

//var _ = AfterSuite(func() {
//	cleanUpCluster()
//})

//var _ = SynchronizedAfterSuite(func() {
//	// Run on all Ginkgo nodes
//
//}, func() {
//	// Run only Ginkgo on node 1
//	cleanUpCluster()
//})
