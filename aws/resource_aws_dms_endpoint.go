package aws

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	dms "github.com/aws/aws-sdk-go/service/databasemigrationservice"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
)

func resourceAwsDmsEndpoint() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsDmsEndpointCreate,
		Read:   resourceAwsDmsEndpointRead,
		Update: resourceAwsDmsEndpointUpdate,
		Delete: resourceAwsDmsEndpointDelete,

		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"certificate_arn": {
				Type:         schema.TypeString,
				Computed:     true,
				Optional:     true,
				ValidateFunc: validateArn,
			},
			"database_name": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"endpoint_arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"endpoint_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validateDmsEndpointId,
			},
			"service_access_role": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"endpoint_type": {
				Type:     schema.TypeString,
				Required: true,
				ValidateFunc: validation.StringInSlice([]string{
					"source",
					"target",
				}, false),
			},
			"engine_name": {
				Type:     schema.TypeString,
				Required: true,
				ValidateFunc: validation.StringInSlice([]string{
					"mysql",
					"oracle",
					"postgres",
					"dynamodb",
					"mariadb",
					"aurora",
					"aurora-postgresql",
					"redshift",
					"sybase",
					"sqlserver",
					"s3",
				}, false),
			},
			"extra_connection_attributes": {
				Type:     schema.TypeString,
				Computed: true,
				Optional: true,
			},
			"kms_key_arn": {
				Type:          schema.TypeString,
				Computed:      true,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"bucket_name", "bucket_folder"},
				ValidateFunc:  validateArn,
			},
			"password": {
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"port": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"server_name": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"ssl_mode": {
				Type:     schema.TypeString,
				Computed: true,
				Optional: true,
				ValidateFunc: validation.StringInSlice([]string{
					"none",
					"require",
					"verify-ca",
					"verify-full",
				}, false),
			},
			"tags": {
				Type:     schema.TypeMap,
				Optional: true,
			},
			"username": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"bucket_name": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"bucket_folder": {
				Type:     schema.TypeString,
				Optional: true,
			},
		},
	}
}

func resourceAwsDmsEndpointCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).dmsconn

	request := &dms.CreateEndpointInput{
		EndpointIdentifier: aws.String(d.Get("endpoint_id").(string)),
		EndpointType:       aws.String(d.Get("endpoint_type").(string)),
		EngineName:         aws.String(d.Get("engine_name").(string)),
		Tags:               dmsTagsFromMap(d.Get("tags").(map[string]interface{})),
	}

	switch d.Get("engine_name").(string) {
	// if dynamodb then add required params
	case "dynamodb":
		request.DynamoDbSettings = &dms.DynamoDbSettings{
			ServiceAccessRoleArn: aws.String(d.Get("service_access_role").(string)),
		}
	case "s3":
		request.S3Settings = &dms.S3Settings{
			BucketName:           aws.String(d.Get("bucket_name").(string)),
			BucketFolder:         aws.String(d.Get("bucket_folder").(string)),
			ServiceAccessRoleArn: aws.String(d.Get("service_access_role").(string)),

			// By default extra variables (should be set):
			CompressionType: aws.String("GZIP"),
			CsvDelimiter:    aws.String(","),
			CsvRowDelimiter: aws.String("\\n"),
		}

		// if extra_connection_attributes is set. Then parse the varaiables.
		if v, ok := d.GetOk("extra_connection_attributes"); ok {
			elems := strings.Split(v.(string), ";")
			if len(elems) > 0 {
				for _, elem := range elems {
					vals := strings.Split(elem, "=")
					if strings.Contains(strings.ToLower(vals[0]), "compressiontype") {
						request.S3Settings.CompressionType = aws.String(vals[1])
					} else if strings.Contains(strings.ToLower(vals[0]), "csvdelimiter") {
						request.S3Settings.CsvDelimiter = aws.String(vals[1])
					} else if strings.Contains(strings.ToLower(vals[0]), "csvrowdelimiter") {
						request.S3Settings.CsvRowDelimiter = aws.String(vals[1])
					}
				}
			}
		}

	default:
		request.Password = aws.String(d.Get("password").(string))
		request.Port = aws.Int64(int64(d.Get("port").(int)))
		request.ServerName = aws.String(d.Get("server_name").(string))
		request.Username = aws.String(d.Get("username").(string))

		if v, ok := d.GetOk("database_name"); ok {
			request.DatabaseName = aws.String(v.(string))
		}
		if v, ok := d.GetOk("extra_connection_attributes"); ok {
			request.ExtraConnectionAttributes = aws.String(v.(string))
		}
		if v, ok := d.GetOk("kms_key_arn"); ok {
			request.KmsKeyId = aws.String(v.(string))
		}
		if v, ok := d.GetOk("ssl_mode"); ok {
			request.SslMode = aws.String(v.(string))
		}
	}

	if v, ok := d.GetOk("certificate_arn"); ok {
		request.CertificateArn = aws.String(v.(string))
	}

	log.Println("[DEBUG] DMS create endpoint:", request)

	err := resource.Retry(5*time.Minute, func() *resource.RetryError {
		if _, err := conn.CreateEndpoint(request); err != nil {
			if awserr, ok := err.(awserr.Error); ok {
				switch awserr.Code() {
				case "AccessDeniedFault":
					return resource.RetryableError(awserr)
				}
			}
			// Didn't recognize the error, so shouldn't retry.
			return resource.NonRetryableError(err)
		}
		// Successful delete
		return nil
	})
	if err != nil {
		return err
	}

	d.SetId(d.Get("endpoint_id").(string))
	return resourceAwsDmsEndpointRead(d, meta)
}

func resourceAwsDmsEndpointRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).dmsconn

	response, err := conn.DescribeEndpoints(&dms.DescribeEndpointsInput{
		Filters: []*dms.Filter{
			{
				Name:   aws.String("endpoint-id"),
				Values: []*string{aws.String(d.Id())}, // Must use d.Id() to work with import.
			},
		},
	})
	if err != nil {
		if dmserr, ok := err.(awserr.Error); ok && dmserr.Code() == "ResourceNotFoundFault" {
			log.Printf("[DEBUG] DMS Replication Endpoint %q Not Found", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	err = resourceAwsDmsEndpointSetState(d, response.Endpoints[0])
	if err != nil {
		return err
	}

	tagsResp, err := conn.ListTagsForResource(&dms.ListTagsForResourceInput{
		ResourceArn: aws.String(d.Get("endpoint_arn").(string)),
	})
	if err != nil {
		return err
	}
	d.Set("tags", dmsTagsToMap(tagsResp.TagList))

	return nil
}

func resourceAwsDmsEndpointUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).dmsconn

	request := &dms.ModifyEndpointInput{
		EndpointArn: aws.String(d.Get("endpoint_arn").(string)),
	}
	hasChanges := false

	if d.HasChange("endpoint_type") {
		request.EndpointType = aws.String(d.Get("endpoint_type").(string))
		hasChanges = true
	}

	if d.HasChange("certificate_arn") {
		request.CertificateArn = aws.String(d.Get("certificate_arn").(string))
		hasChanges = true
	}

	if d.HasChange("database_name") {
		request.DatabaseName = aws.String(d.Get("database_name").(string))
		hasChanges = true
	}

	if d.HasChange("engine_name") {
		request.EngineName = aws.String(d.Get("engine_name").(string))
		hasChanges = true
	}

	switch d.Get("engine_name").(string) {
	case "dynamodb":
		if d.HasChange("service_access_role") {
			request.DynamoDbSettings = &dms.DynamoDbSettings{
				ServiceAccessRoleArn: aws.String(d.Get("service_access_role").(string)),
			}
			hasChanges = true
		}
	case "s3":
		if d.HasChange("service_access_role") || d.HasChange("bucket_name") || d.HasChange("bucket_folder") || d.HasChange("extra_connection_attributes") {
			request.S3Settings = &dms.S3Settings{
				ServiceAccessRoleArn: aws.String(d.Get("service_access_role").(string)),
				BucketFolder:         aws.String(d.Get("bucket_folder").(string)),
				BucketName:           aws.String(d.Get("bucket_name").(string)),
			}

			elems := strings.Split(d.Get("extra_connection_attributes").(string), ";")
			if len(elems) > 0 {
				for _, elem := range elems {
					vals := strings.Split(elem, "=")
					if strings.Contains(strings.ToLower(vals[0]), "compressiontype") {
						request.S3Settings.CompressionType = aws.String(vals[1])
					} else if strings.Contains(strings.ToLower(vals[0]), "csvdelimiter") {
						request.S3Settings.CsvDelimiter = aws.String(vals[1])
					} else if strings.Contains(strings.ToLower(vals[0]), "csvrowdelimiter") {
						request.S3Settings.CsvRowDelimiter = aws.String(vals[1])
					}
				}
			}

			hasChanges = true

			goto DONE
		}
	default:
		if d.HasChange("extra_connection_attributes") {
			request.ExtraConnectionAttributes = aws.String(d.Get("extra_connection_attributes").(string))
			hasChanges = true
		}
	}

	if d.HasChange("password") {
		request.Password = aws.String(d.Get("password").(string))
		hasChanges = true
	}

	if d.HasChange("port") {
		request.Port = aws.Int64(int64(d.Get("port").(int)))
		hasChanges = true
	}

	if d.HasChange("server_name") {
		request.ServerName = aws.String(d.Get("server_name").(string))
		hasChanges = true
	}

	if d.HasChange("ssl_mode") {
		request.SslMode = aws.String(d.Get("ssl_mode").(string))
		hasChanges = true
	}

	if d.HasChange("username") {
		request.Username = aws.String(d.Get("username").(string))
		hasChanges = true
	}

	if d.HasChange("tags") {
		err := dmsSetTags(d.Get("endpoint_arn").(string), d, meta)
		if err != nil {
			return err
		}
	}

DONE:
	if hasChanges {
		log.Println("[DEBUG] DMS update endpoint:", request)

		_, err := conn.ModifyEndpoint(request)
		if err != nil {
			return err
		}

		return resourceAwsDmsEndpointRead(d, meta)
	}

	return nil
}

func resourceAwsDmsEndpointDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).dmsconn

	request := &dms.DeleteEndpointInput{
		EndpointArn: aws.String(d.Get("endpoint_arn").(string)),
	}

	log.Printf("[DEBUG] DMS delete endpoint: %#v", request)

	_, err := conn.DeleteEndpoint(request)
	if err != nil {
		return err
	}

	return nil
}

func resourceAwsDmsEndpointSetState(d *schema.ResourceData, endpoint *dms.Endpoint) error {
	d.SetId(*endpoint.EndpointIdentifier)

	d.Set("certificate_arn", endpoint.CertificateArn)
	d.Set("endpoint_arn", endpoint.EndpointArn)
	d.Set("endpoint_id", endpoint.EndpointIdentifier)
	// For some reason the AWS API only accepts lowercase type but returns it as uppercase
	d.Set("endpoint_type", strings.ToLower(*endpoint.EndpointType))
	d.Set("engine_name", endpoint.EngineName)

	switch *endpoint.EngineName {
	case "dynamodb":
		if endpoint.DynamoDbSettings != nil {
			d.Set("service_access_role", endpoint.DynamoDbSettings.ServiceAccessRoleArn)
		} else {
			d.Set("service_access_role", "")
		}
	case "s3":
		if endpoint.S3Settings != nil {
			d.Set("service_access_role", endpoint.S3Settings.ServiceAccessRoleArn)
			d.Set("bucket_folder", endpoint.S3Settings.BucketFolder)
			d.Set("bucket_name", endpoint.S3Settings.BucketName)
			d.Set("extra_connection_attributes",
				aws.String(fmt.Sprintf("compressionType=%s;csvDelimiter=%s;csvRowDelimiter=%s",
					*endpoint.S3Settings.CompressionType, *endpoint.S3Settings.CsvDelimiter, *endpoint.S3Settings.CsvRowDelimiter)))
		} else {
			d.Set("service_access_role", "")
			d.Set("bucket_folder", "")
			d.Set("bucket_name", "")
			d.Set("extra_connection_attributes", "")
		}
	default:
		d.Set("database_name", endpoint.DatabaseName)
		d.Set("extra_connection_attributes", endpoint.ExtraConnectionAttributes)
		d.Set("port", endpoint.Port)
		d.Set("server_name", endpoint.ServerName)
		d.Set("username", endpoint.Username)
		d.Set("kms_key_arn", endpoint.KmsKeyId)
		d.Set("ssl_mode", endpoint.SslMode)
	}

	return nil
}
