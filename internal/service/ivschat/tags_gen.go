// Code generated by internal/generate/tags/main.go; DO NOT EDIT.
package ivschat

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ivschat"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
)

// ListTags lists ivschat service tags.
// The identifier is typically the Amazon Resource Name (ARN), although
// it may also be a different identifier depending on the service.
func ListTags(ctx context.Context, conn *ivschat.Client, identifier string) (tftags.KeyValueTags, error) {
	input := &ivschat.ListTagsForResourceInput{
		ResourceArn: aws.String(identifier),
	}

	output, err := conn.ListTagsForResource(ctx, input)

	if err != nil {
		return tftags.New(nil), err
	}

	return KeyValueTags(output.Tags), nil
}

// map[string]string handling

// Tags returns ivschat service tags.
func Tags(tags tftags.KeyValueTags) map[string]string {
	return tags.Map()
}

// KeyValueTags creates KeyValueTags from ivschat service tags.
func KeyValueTags(tags map[string]string) tftags.KeyValueTags {
	return tftags.New(tags)
}

// UpdateTags updates ivschat service tags.
// The identifier is typically the Amazon Resource Name (ARN), although
// it may also be a different identifier depending on the service.
func UpdateTags(ctx context.Context, conn *ivschat.Client, identifier string, oldTagsMap interface{}, newTagsMap interface{}) error {
	oldTags := tftags.New(oldTagsMap)
	newTags := tftags.New(newTagsMap)

	if removedTags := oldTags.Removed(newTags); len(removedTags) > 0 {
		input := &ivschat.UntagResourceInput{
			ResourceArn: aws.String(identifier),
			TagKeys:     removedTags.IgnoreAWS().Keys(),
		}

		_, err := conn.UntagResource(ctx, input)

		if err != nil {
			return fmt.Errorf("untagging resource (%s): %w", identifier, err)
		}
	}

	if updatedTags := oldTags.Updated(newTags); len(updatedTags) > 0 {
		input := &ivschat.TagResourceInput{
			ResourceArn: aws.String(identifier),
			Tags:        Tags(updatedTags.IgnoreAWS()),
		}

		_, err := conn.TagResource(ctx, input)

		if err != nil {
			return fmt.Errorf("tagging resource (%s): %w", identifier, err)
		}
	}

	return nil
}
