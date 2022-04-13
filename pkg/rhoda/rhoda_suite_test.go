package rhoda_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRhoda(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rhoda Suite")
}
