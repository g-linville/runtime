package cli

import (
	"fmt"
	"strings"

	cli "github.com/acorn-io/acorn/pkg/cli/builder"
	"github.com/acorn-io/acorn/pkg/client"
	"github.com/acorn-io/acorn/pkg/tags"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
)

func NewImageDelete(c CommandContext) *cobra.Command {
	cmd := cli.Command(&ImageDelete{client: c.ClientFactory}, cobra.Command{
		Use:               "rm [IMAGE_NAME...]",
		Example:           `acorn image rm my-image`,
		SilenceUsage:      true,
		Short:             "Delete an Image",
		ValidArgsFunction: newCompletion(c.ClientFactory, imagesCompletion(true)).complete,
	})
	return cmd
}

type ImageDelete struct {
	client ClientFactory
	Force  bool `usage:"Force Delete" short:"f"`
}

func (a *ImageDelete) Run(cmd *cobra.Command, args []string) error {
	c, err := a.client.CreateDefault()
	if err != nil {
		return err
	}

	for _, image := range args {
		opts := []name.Option{name.WithDefaultRegistry("")}

		if strings.HasPrefix("sha256:", image) || tags.SHAPermissivePrefixPattern.MatchString(image) {
			opts = append(opts, name.WithDefaultTag(""))
		}

		// normalize image name (adding :latest if no tag is specified and it's not a digest or potential ID)
		ref, err := name.ParseReference(image, opts...)
		if err != nil {
			return err
		}
		deleted, err := c.ImageDelete(cmd.Context(), strings.TrimSuffix(ref.Name(), ":"), &client.ImageDeleteOptions{Force: a.Force})
		if err != nil {
			return fmt.Errorf("deleting %s: %w", image, err)
		}
		if deleted != nil {
			fmt.Println(image)
		} else {
			fmt.Printf("Error: No such image: %s\n", image)
		}
	}

	return nil
}
