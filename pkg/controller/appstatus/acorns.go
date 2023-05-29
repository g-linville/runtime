package appstatus

import (
	"strconv"

	v1 "github.com/acorn-io/acorn/pkg/apis/internal.acorn.io/v1"
	"github.com/acorn-io/acorn/pkg/labels"
	"github.com/acorn-io/acorn/pkg/ports"
	"github.com/acorn-io/acorn/pkg/publicname"
	name2 "github.com/acorn-io/baaah/pkg/name"
	"github.com/acorn-io/baaah/pkg/router"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func (a *appStatusRenderer) readAcorns() error {
	// reset state
	a.app.Status.AppStatus.Acorns = map[string]v1.AcornStatus{}

	for acornName := range a.app.Status.AppSpec.Acorns {
		s := v1.AcornStatus{
			CommonStatus: v1.CommonStatus{
				Defined:      ports.IsLinked(a.app, acornName),
				LinkOverride: ports.LinkService(a.app, acornName),
			},
		}

		if s.LinkOverride != "" {
			s.UpToDate = true
			s.Ready, _ = a.isServiceReady(acornName)
			a.app.Status.AppStatus.Acorns[acornName] = s
			continue
		}

		acorn := &v1.AppInstance{}
		err := a.c.Get(a.ctx, router.Key(a.app.Namespace, name2.SafeHashConcatName(a.app.Name, acornName)), acorn)
		if apierrors.IsNotFound(err) {
			a.app.Status.AppStatus.Acorns[acornName] = s
			continue
		} else if err != nil {
			return err
		}

		s.Defined = true
		s.UpToDate = acorn.Annotations[labels.AcornAppGeneration] == strconv.Itoa(int(a.app.Generation))
		s.Ready = s.UpToDate && acorn.Status.Ready
		s.AcornName = publicname.Get(acorn)
		a.app.Status.AppStatus.Acorns[acornName] = s
	}

	return nil
}