package rulerequest

import (
	"context"
	"errors"
	"fmt"

	apiv1 "github.com/acorn-io/acorn/pkg/apis/api.acorn.io/v1"
	v1 "github.com/acorn-io/acorn/pkg/apis/internal.acorn.io/v1"
	"github.com/acorn-io/acorn/pkg/cli/builder/table"
	"github.com/acorn-io/acorn/pkg/client"
	"github.com/acorn-io/acorn/pkg/imageallowrules"
	"github.com/acorn-io/acorn/pkg/images"
	"github.com/acorn-io/acorn/pkg/prompt"
	"github.com/acorn-io/acorn/pkg/run"
	"github.com/acorn-io/acorn/pkg/tables"
	"github.com/acorn-io/acorn/pkg/tags"
	"github.com/pterm/pterm"
)

func PromptRun(ctx context.Context, c client.Client, dangerous bool, image string, opts client.AppRunOptions) (*apiv1.App, error) {
	app, err := c.AppRun(ctx, image, &opts)

	// App requested permissions
	if permErr := (*client.ErrRulesNeeded)(nil); errors.As(err, &permErr) {
		if ok, promptErr := handleDangerous(dangerous, permErr.Permissions); promptErr != nil {
			return nil, fmt.Errorf("%s: %w", promptErr.Error(), err)
		} else if ok {
			opts.Permissions = permErr.Permissions
			app, err = c.AppRun(ctx, image, &opts)
		}
	}

	// ImageAllowRules are enabled and this image is not allowed
	if naErr := (*imageallowrules.ErrImageNotAllowed)(nil); errors.As(err, &naErr) {
		err.(*imageallowrules.ErrImageNotAllowed).Image = image

		// We're checking for images first, since we could run existing images by ID
		var il []apiv1.Image
		il, err = c.ImageList(ctx)
		if err != nil {
			return nil, err
		}
		var existingImg *apiv1.Image
		var existingImgTag, existingImgName string

		existingImg, existingImgTag, err = images.FindImageMatch(apiv1.ImageList{Items: il}, image)
		if err != nil && !errors.As(err, &images.ErrImageNotFound{}) {
			return nil, err
		} else if err == nil {
			image = existingImg.Name
			existingImgName = existingImg.Name
			if existingImgTag != "" {
				image = existingImgTag
			}
		}

		// Prompt user to create an simple IAR for this image
		if choice, promptErr := handleNotAllowed(dangerous, image); promptErr != nil {
			return nil, fmt.Errorf("%s: %w", promptErr.Error(), err)
		} else if choice != "NO" {
			iarErr := createImageAllowRule(ctx, c, image, choice, existingImgName) // existingImgName to ensure that this exact image ID is allowed in addition to whatever pattern we're allowing
			if iarErr != nil {
				return nil, iarErr
			}
			app, err = c.AppRun(ctx, image, &opts)
		}
	}
	return app, err
}

func PromptUpdate(ctx context.Context, c client.Client, dangerous bool, name string, opts client.AppUpdateOptions) (*apiv1.App, error) {
	app, err := c.AppUpdate(ctx, name, &opts)
	if permErr := (*client.ErrRulesNeeded)(nil); errors.As(err, &permErr) {
		if ok, promptErr := handleDangerous(dangerous, permErr.Permissions); promptErr != nil {
			return nil, fmt.Errorf("%s: %w", promptErr.Error(), err)
		} else if ok {
			opts.Permissions = permErr.Permissions
			app, err = c.AppUpdate(ctx, name, &opts)
		}
	}
	return app, err
}

func handleDangerous(dangerous bool, perms []v1.Permissions) (bool, error) {
	if dangerous {
		return true, nil
	}

	requests := ToRuleRequests(perms)

	pterm.Warning.Println(
		`This application would like to request the following runtime permissions.
This could be VERY DANGEROUS to the cluster if you do not trust this
application. If you are unsure say no.`)
	pterm.Println()

	writer := table.NewWriter(tables.RuleRequests, false, "")
	for _, request := range requests {
		writer.Write(request)
	}

	if err := writer.Close(); err != nil {
		return false, err
	}

	pterm.Println()
	return prompt.Bool("Do you want to allow this app to have these (POTENTIALLY DANGEROUS) permissions?", false)
}

func handleNotAllowed(dangerous bool, image string) (string, error) {
	if dangerous {
		return string(imageallowrules.SimpleImageScopeExact), nil
	}

	pterm.Warning.Printfln(
		`This application would like to use the image '%s'.
This image is not trusted by any image allow rules in this project.
This could be VERY DANGEROUS to the cluster if you do not trust this
application. If you are unsure say no.`, image)

	// The below code is super ugly, but it's the only non-overengineered way to preserve the order of the
	// choices since Go doesn't guarantee the order when looping over a map. If you have a better way, please push it :)
	var choiceMap map[string]string
	var choices []string

	if tags.SHAPattern.MatchString(image) {
		choices = []string{
			"NO",
			"yes (this ID or SHA only)",
		}

		choiceMap = map[string]string{
			choices[0]: "NO",
			choices[1]: string(imageallowrules.SimpleImageScopeExact),
		}
	} else {
		choices = []string{
			"NO",
			"yes (this tag only)",
			"repository (all images in this repository)",
			"registry (all images in this registry)",
			"all (all images out there)",
		}

		choiceMap = map[string]string{
			choices[0]: "NO",
			choices[1]: string(imageallowrules.SimpleImageScopeExact),
			choices[2]: string(imageallowrules.SimpleImageScopeRepository),
			choices[3]: string(imageallowrules.SimpleImageScopeRegistry),
			choices[4]: string(imageallowrules.SimpleImageScopeAll),
		}
	}

	pterm.Println()
	choice, err := prompt.Choice("Do you want to allow this app to use this (POTENTIALLY DANGEROUS) image?", choices, "NO")
	return choiceMap[choice], err
}

func createImageAllowRule(ctx context.Context, c client.Client, image, choice string, extraExactMatches ...string) error {
	iar, err := imageallowrules.GenerateSimpleAllowRule(c.GetProject(), run.NameGenerator.Generate(), image, choice)
	if err != nil {
		return fmt.Errorf("error generating ImageAllowRule: %w", err)
	}

	for _, extra := range extraExactMatches {
		if extra != image && extra != "" {
			iar.Images = append(iar.Images, extra)
		}
	}

	cli, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("error getting client: %w", err)
	}
	if err := cli.Create(ctx, iar); err != nil {
		return fmt.Errorf("error creating ImageAllowRule: %w", err)
	}
	pterm.Success.Printf("Created ImageAllowRules %s/%s with image scope %s\n", iar.Namespace, iar.Name, iar.Images[0])
	return nil
}
