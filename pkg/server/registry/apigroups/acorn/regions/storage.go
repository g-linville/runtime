package regions

import (
	"time"

	apiv1 "github.com/acorn-io/acorn/pkg/apis/api.acorn.io/v1"
	"github.com/acorn-io/acorn/pkg/tables"
	"github.com/acorn-io/mink/pkg/stores"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/registry/rest"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewStorage(c kclient.WithWatch) rest.Storage {
	s := &strategy{metav1.NewTime(time.Now())}
	return stores.NewBuilder(c.Scheme(), &apiv1.Region{}).
		WithGet(s).
		WithList(s).
		WithTableConverter(tables.RegionConverter).
		Build()
}
