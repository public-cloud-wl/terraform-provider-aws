package aws

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/service/secretsmanager/waiter"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/tfresource"
)

func resourceAwsSecretsManagerSecretVersion() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsSecretsManagerSecretVersionCreate,
		Read:   resourceAwsSecretsManagerSecretVersionRead,
		Update: resourceAwsSecretsManagerSecretVersionUpdate,
		Delete: resourceAwsSecretsManagerSecretVersionDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"secret_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"secret_string": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				Sensitive:     true,
				ConflictsWith: []string{"secret_binary"},
			},
			"secret_binary": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				Sensitive:     true,
				ConflictsWith: []string{"secret_string"},
			},
			"version_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"version_stages": {
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func resourceAwsSecretsManagerSecretVersionCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).secretsmanagerconn
	secretID := d.Get("secret_id").(string)

	input := &secretsmanager.PutSecretValueInput{
		SecretId: aws.String(secretID),
	}

	if v, ok := d.GetOk("secret_string"); ok {
		input.SecretString = aws.String(v.(string))
	}

	if v, ok := d.GetOk("secret_binary"); ok {
		vs := []byte(v.(string))

		if !isBase64Encoded(vs) {
			return fmt.Errorf("expected base64 in secret_binary")
		}

		var err error
		input.SecretBinary, err = base64.StdEncoding.DecodeString(v.(string))

		if err != nil {
			return fmt.Errorf("error decoding secret binary value: %s", err)
		}
	}

	if v, ok := d.GetOk("version_stages"); ok {
		input.VersionStages = expandStringSet(v.(*schema.Set))
	}

	log.Printf("[DEBUG] Putting Secrets Manager Secret %q value", secretID)
	output, err := conn.PutSecretValue(input)
	if err != nil {
		return fmt.Errorf("error putting Secrets Manager Secret value: %s", err)
	}

	d.SetId(fmt.Sprintf("%s|%s", secretID, aws.StringValue(output.VersionId)))

	return resourceAwsSecretsManagerSecretVersionRead(d, meta)
}

func resourceAwsSecretsManagerSecretVersionRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).secretsmanagerconn

	secretID, versionID, err := decodeSecretsManagerSecretVersionID(d.Id())
	if err != nil {
		return err
	}

	input := &secretsmanager.GetSecretValueInput{
		SecretId:  aws.String(secretID),
		VersionId: aws.String(versionID),
	}

	var output *secretsmanager.GetSecretValueOutput

	err = resource.Retry(waiter.PropagationTimeout, func() *resource.RetryError {
		var err error

		output, err = conn.GetSecretValue(input)

		if d.IsNewResource() && tfawserr.ErrCodeEquals(err, secretsmanager.ErrCodeResourceNotFoundException) {
			return resource.RetryableError(err)
		}

		if d.IsNewResource() && tfawserr.ErrMessageContains(err, secretsmanager.ErrCodeInvalidRequestException, "You can’t perform this operation on the secret because it was deleted") {
			return resource.RetryableError(err)
		}

		if err != nil {
			return resource.NonRetryableError(err)
		}

		return nil
	})

	if tfresource.TimedOut(err) {
		output, err = conn.GetSecretValue(input)
	}

	if !d.IsNewResource() && tfawserr.ErrCodeEquals(err, secretsmanager.ErrCodeResourceNotFoundException) {
		log.Printf("[WARN] Secrets Manager Secret Version (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if !d.IsNewResource() && tfawserr.ErrMessageContains(err, secretsmanager.ErrCodeInvalidRequestException, "You can’t perform this operation on the secret because it was deleted") {
		log.Printf("[WARN] Secrets Manager Secret Version (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err != nil {
		return fmt.Errorf("error reading Secrets Manager Secret Version (%s): %w", d.Id(), err)
	}

	if output == nil {
		return fmt.Errorf("error reading Secrets Manager Secret Version (%s): empty response", d.Id())
	}

	d.Set("secret_id", secretID)
	d.Set("secret_string", output.SecretString)
	d.Set("secret_binary", base64Encode(output.SecretBinary))
	d.Set("version_id", output.VersionId)
	d.Set("arn", output.ARN)

	if err := d.Set("version_stages", flattenStringList(output.VersionStages)); err != nil {
		return fmt.Errorf("error setting version_stages: %s", err)
	}

	return nil
}

func resourceAwsSecretsManagerSecretVersionUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).secretsmanagerconn

	secretID, versionID, err := decodeSecretsManagerSecretVersionID(d.Id())
	if err != nil {
		return err
	}

	o, n := d.GetChange("version_stages")
	os := o.(*schema.Set)
	ns := n.(*schema.Set)
	stagesToAdd := ns.Difference(os).List()
	stagesToRemove := os.Difference(ns).List()

	var describedSecret *secretsmanager.DescribeSecretOutput
	awsPreviousVersionID := aws.String(versionID)

	for _, stage := range stagesToAdd {
		input := &secretsmanager.UpdateSecretVersionStageInput{
			MoveToVersionId: aws.String(versionID),
			SecretId:        aws.String(secretID),
			VersionStage:    aws.String(stage.(string)),
		}

		if stage.(string) == "AWSCURRENT" {
			log.Printf("[DEBUG] Going to set AWSCURRENT staging label for secret %q version %q", secretID, versionID)

			// NOTE: Cache it to prevent calling it more than once
			if describedSecret == nil {
				describedSecret, err = conn.DescribeSecret(&secretsmanager.DescribeSecretInput{SecretId: aws.String(secretID)})
				if err != nil {
					return fmt.Errorf("error updating Secrets Manager Secret %q Version Stage %q: %s", secretID, stage.(string), err)
				}

				var awsCurrentStageVersionID *string

				var nextToken *string = nil

			loopListVersionIDsPagination:
				for {
					output, err := conn.ListSecretVersionIds(&secretsmanager.ListSecretVersionIdsInput{
						NextToken: nextToken,
						SecretId:  aws.String(secretID),
					})
					if err != nil {
						return fmt.Errorf("error updating Secrets Manager Secret %q Version Stage %q: %s", secretID, stage.(string), err)
					}

					for _, version := range output.Versions {
						for _, versionStage := range version.VersionStages {
							// NOTE: Even though AWS API can return multiple version stages to a single version,
							// there's only one `AWSCURRENT`, therefore return early.
							if versionStage != nil && *versionStage == "AWSCURRENT" {
								awsCurrentStageVersionID = version.VersionId
								break loopListVersionIDsPagination
							}
						}
					}

					if output.NextToken == nil {
						break
					}

					nextToken = output.NextToken
				}

				input.RemoveFromVersionId = awsCurrentStageVersionID
				log.Printf(
					"[DEBUG] Going to move AWSCURRENT staging label for secret %q from version: %q to version %q",
					secretID,
					*awsCurrentStageVersionID,
					versionID,
				)
			}

		}
		log.Printf("[DEBUG] Updating Secrets Manager Secret Version Stage: %s", input)
		_, err := conn.UpdateSecretVersionStage(input)
		if err != nil {
			return fmt.Errorf("error updating Secrets Manager Secret %q Version Stage %q: %s", secretID, stage.(string), err)
		}

		// NOTE: After changing the `AWSCURRENT`, the previous `AWSCURRENT` is now `AWSPREVIOUS`,
		// which we'll need to remove previous labels.
		awsPreviousVersionID = input.RemoveFromVersionId
	}

	for _, stage := range stagesToRemove {
		// InvalidParameterException: You can only move staging label AWSCURRENT to a different secret version. It can’t be completely removed.
		if stage.(string) == "AWSCURRENT" {
			log.Printf("[INFO] Skipping removal of AWSCURRENT staging label for secret %q version %q", secretID, versionID)
			continue
		}
		input := &secretsmanager.UpdateSecretVersionStageInput{
			RemoveFromVersionId: awsPreviousVersionID,
			SecretId:            aws.String(secretID),
			VersionStage:        aws.String(stage.(string)),
		}
		log.Printf("[DEBUG] Updating Secrets Manager Secret Version Stage: %s", input)
		_, err := conn.UpdateSecretVersionStage(input)
		if err != nil {
			return fmt.Errorf("error updating Secrets Manager Secret %q Version Stage %q: %s", secretID, stage.(string), err)
		}
	}

	return resourceAwsSecretsManagerSecretVersionRead(d, meta)
}

func resourceAwsSecretsManagerSecretVersionDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).secretsmanagerconn

	secretID, versionID, err := decodeSecretsManagerSecretVersionID(d.Id())
	if err != nil {
		return err
	}

	if v, ok := d.GetOk("version_stages"); ok {
		for _, stage := range v.(*schema.Set).List() {
			// InvalidParameterException: You can only move staging label AWSCURRENT to a different secret version. It can’t be completely removed.
			if stage.(string) == "AWSCURRENT" {
				log.Printf("[WARN] Cannot remove AWSCURRENT staging label, which may leave the secret %q version %q active", secretID, versionID)
				continue
			}
			input := &secretsmanager.UpdateSecretVersionStageInput{
				RemoveFromVersionId: aws.String(versionID),
				SecretId:            aws.String(secretID),
				VersionStage:        aws.String(stage.(string)),
			}
			log.Printf("[DEBUG] Updating Secrets Manager Secret Version Stage: %s", input)
			_, err := conn.UpdateSecretVersionStage(input)
			if err != nil {
				if isAWSErr(err, secretsmanager.ErrCodeResourceNotFoundException, "") {
					return nil
				}
				if isAWSErr(err, secretsmanager.ErrCodeInvalidRequestException, "You can’t perform this operation on the secret because it was deleted") {
					return nil
				}
				return fmt.Errorf("error updating Secrets Manager Secret %q Version Stage %q: %s", secretID, stage.(string), err)
			}
		}
	}

	return nil
}

func decodeSecretsManagerSecretVersionID(id string) (string, string, error) {
	idParts := strings.Split(id, "|")
	if len(idParts) != 2 {
		return "", "", fmt.Errorf("expected ID in format SecretID|VersionID, received: %s", id)
	}
	return idParts[0], idParts[1], nil
}
