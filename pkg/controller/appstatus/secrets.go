package appstatus

import (
	"fmt"
	"strconv"

	v1 "github.com/acorn-io/acorn/pkg/apis/internal.acorn.io/v1"
	"github.com/acorn-io/acorn/pkg/labels"
	"github.com/acorn-io/acorn/pkg/publicname"
	"github.com/acorn-io/acorn/pkg/ref"
	"github.com/acorn-io/baaah/pkg/router"
	"github.com/acorn-io/baaah/pkg/typed"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func linkedSecret(app *v1.AppInstance, name string) string {
	if name == "" {
		return ""
	}

	for _, binding := range app.Spec.Secrets {
		if binding.Target == name {
			return binding.Secret
		}
	}

	return ""
}

func (a *appStatusRenderer) readSecrets() (err error) {
	var (
		existingStatus = a.app.Status.AppStatus.Secrets
	)
	// reset state
	a.app.Status.AppStatus.Secrets = map[string]v1.SecretStatus{}

	for secretName, secretDef := range a.app.Status.AppSpec.Secrets {
		s := v1.SecretStatus{
			CommonStatus: v1.CommonStatus{
				LinkOverride:          linkedSecret(a.app, secretName),
				ErrorMessages:         existingStatus[secretName].LookupErrors,
				TransitioningMessages: existingStatus[secretName].LookupTransitioning,
			},
		}

		secret := &corev1.Secret{}
		if err := ref.Lookup(a.ctx, a.c, secret, a.app.Status.Namespace, secretName); apierrors.IsNotFound(err) {
			a.app.Status.AppStatus.Secrets[secretName] = s
			continue
		} else if err != nil {
			return err
		}

		s.UpToDate = secret.Annotations[labels.AcornAppGeneration] == strconv.Itoa(int(a.app.Generation))
		s.Defined = true
		s.Ready = true

		sourceSecret := &corev1.Secret{}
		if err := a.c.Get(a.ctx, router.Key(a.app.Namespace, secret.Labels[labels.AcornSecretSourceName]), sourceSecret); apierrors.IsNotFound(err) {
			a.app.Status.AppStatus.Secrets[secretName] = s
			continue
		} else if err != nil {
			return err
		}

		s.SecretName = publicname.Get(sourceSecret)
		if secretDef.Type == string(v1.SecretTypeGenerated) && secretDef.Params["job"] != "" {
			s.JobName = fmt.Sprint(secretDef.Params["job"])
			s.JobReady, err = a.isJobReady(s.JobName)
			if err != nil {
				return err
			}
		} else {
			s.JobReady = true
		}

		s.Ready = s.Ready && s.JobReady
		s.DataKeys = typed.SortedKeys(sourceSecret.Data)

		a.app.Status.AppStatus.Secrets[secretName] = s
	}

	return nil
}