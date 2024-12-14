package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
	"github.com/refractionPOINT/lc-extension/server/webserver"

	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"github.com/go-redis/redis/v8"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This is the definition of a Cloud Run service
// we can use to create a new service.
type CloudRunServiceDefinition struct {
	Image        string   `json:"image"`
	Env          []string `json:"env"`
	CPU          string   `json:"cpu"`
	Memory       string   `json:"memory"`
	MinInstances int32    `json:"min_instances"`
	MaxInstances int32    `json:"max_instances"`
	Timeout      int32    `json:"timeout"`
}

type CloudRunMultiplexer struct {
	core.Extension
	limacharlie.LCLoggerGCP

	redisClient *redis.Client

	// The project ID where we will provision new Cloud Run services.
	provisionProjectID string
	provisionRegion    string

	// The definition of the Cloud Run service we will use to create new services.
	serviceDefinition CloudRunServiceDefinition

	// HTTP Client to use to forward requests to the service.
	httpClient *http.Client
}

var Extension *CloudRunMultiplexer

func main() {
	// Because this will be configured entirely through environment variables,
	// we will parse the LC_REQUEST_SCHEMA environment variable to get the
	// schema of the requests this Extension will receive. We do the same with
	// the LC_CONFIG_SCHEMA environment variable to get the schema of the
	// configuration this Extension will receive.
	// Finally do the same with LC_EXTENSION_NAME to get the name of the
	// Extension this multiplexer will be proxying requests for and with
	// LC_VIEW_SCHEMA and LC_REQUIRED_EVENTS.
	rs := os.Getenv("LC_REQUEST_SCHEMA")
	if rs == "" {
		panic("LC_REQUEST_SCHEMA is not set")
	}
	reqSchema := map[string]common.RequestSchema{}
	if err := json.Unmarshal([]byte(rs), &reqSchema); err != nil {
		panic(err)
	}
	cs := os.Getenv("LC_CONFIG_SCHEMA")
	if cs == "" {
		panic("LC_CONFIG_SCHEMA is not set")
	}
	configSchema := common.SchemaObject{}
	if err := json.Unmarshal([]byte(cs), &configSchema); err != nil {
		panic(err)
	}
	en := os.Getenv("LC_EXTENSION_NAME")
	if en == "" {
		panic("LC_EXTENSION_NAME is not set")
	}
	vs := os.Getenv("LC_VIEW_SCHEMA")
	if vs == "" {
		panic("LC_VIEW_SCHEMA is not set")
	}
	viewsSchema := []common.View{}
	if err := json.Unmarshal([]byte(vs), &viewsSchema); err != nil {
		panic(err)
	}
	re := os.Getenv("LC_REQUIRED_EVENTS")
	if re == "" {
		panic("LC_REQUIRED_EVENTS is not set")
	}
	requiredEvents := []common.EventName{}
	if err := json.Unmarshal([]byte(re), &requiredEvents); err != nil {
		panic(err)
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_ADDR"),
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		panic(err)
	}
	serviceDefinition := CloudRunServiceDefinition{}
	if err := json.Unmarshal([]byte(os.Getenv("SERVICE_DEFINITION")), &serviceDefinition); err != nil {
		panic(err)
	}
	Extension = &CloudRunMultiplexer{
		core.Extension{
			ExtensionName:  en,
			SecretKey:      os.Getenv("SHARED_SECRET"),
			ConfigSchema:   configSchema,
			RequestSchema:  reqSchema,
			ViewsSchema:    viewsSchema,
			RequiredEvents: requiredEvents,
		},
		limacharlie.LCLoggerGCP{},
		redisClient,
		os.Getenv("PROVISION_PROJECT_ID"),
		os.Getenv("PROVISION_REGION"),
		serviceDefinition,
		&http.Client{
			Timeout: 10 * time.Minute,
		},
	}

	// We must assemble the callbacks for this Extension from the
	// configuration of the Extension. We keep them generic so that
	// we can just be a passthrough for any Extension.
	requestHandlers := map[common.ActionName]core.RequestCallback{}
	for name := range Extension.RequestSchema {
		requestHandlers[name] = core.RequestCallback{
			RequestStruct: nil, // By keeping this nil, we will unmarshal into a dict.
			Callback: func(ctx context.Context, params core.RequestCallbackParams) common.Response {
				return Extension.OnGenericRequest(ctx, name, params)
			},
		}
	}

	// Dynamically add the request handlers for the events.
	eventHandlers := map[common.EventName]core.EventCallback{
		common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
			return Extension.OnGenericEvent(ctx, common.EventTypes.Subscribe, params)
		},
		common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
			return Extension.OnGenericEvent(ctx, common.EventTypes.Unsubscribe, params)
		},
	}
	for _, event := range Extension.RequiredEvents {
		eventHandlers[event] = func(ctx context.Context, params core.EventCallbackParams) common.Response {
			return Extension.OnGenericEvent(ctx, event, params)
		}
	}

	// Callbacks receiving webhooks from LimaCharlie.
	Extension.Callbacks = core.ExtensionCallbacks{
		// When a user changes a config for this Extension, you will be asked to validate it.
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			Extension.Info(fmt.Sprintf("validate config from %s", org.GetOID()))
			// Resolve the Cloud Run service for this OID.
			_, _, serviceURL, err := Extension.getService(org.GetOID())
			if err != nil {
				return common.Response{
					Error: fmt.Sprintf("failed to get service: %v", err),
				}
			}

			// Forward the request.
			response, err := Extension.forwardConfigValidation(ctx, org, serviceURL, config)
			if err != nil {
				return common.Response{
					Error: fmt.Sprintf("failed to forward config validation: %v", err),
				}
			}
			// Return the response.
			return *response
		},
		RequestHandlers: requestHandlers,
		// Events occuring in LimaCharlie that we need to be made aware of.
		EventHandlers: eventHandlers,
		ErrorHandler: func(err *common.ErrorReportMessage) {
			Extension.Error(fmt.Sprintf("error: %s", err.Error))
		},
	}
	// Start processing.
	if err := Extension.Init(); err != nil {
		panic(err)
	}

	webserver.RunExtension(Extension)
}

// This example defines a simple http handler that can now be used
// as an entry point to a Cloud Function. See /server/webserver for a
// useful helper to run the handler as a webserver in a container.
func Process(w http.ResponseWriter, r *http.Request) {
	Extension.ServeHTTP(w, r)
}

func (e *CloudRunMultiplexer) Init() error {
	// Initialize the Extension core.
	if err := e.Extension.Init(); err != nil {
		return err
	}

	return nil
}

func (e *CloudRunMultiplexer) OnGenericRequest(ctx context.Context, actionName common.ActionName, params core.RequestCallbackParams) common.Response {
	// If we needed to do custom processing, we would do it here.
	// For now, we will just forward the request to the service.
	response, err := e.forwardRequest(ctx, actionName, params)
	if err != nil {
		return common.Response{
			Error: fmt.Sprintf("failed to forward request: %v", err),
		}
	}
	return *response
}

func (e *CloudRunMultiplexer) OnGenericEvent(ctx context.Context, eventName common.EventName, params core.EventCallbackParams) common.Response {
	// If it is a subscribe event, we need to create a new service.
	// If it is an unsubscribe event, we need to delete the service.
	if eventName == common.EventTypes.Subscribe {
		_, _, err := e.createService(params.Org.GetOID())
		if err != nil {
			return common.Response{
				Error: fmt.Sprintf("failed to create service: %v", err),
			}
		}
	} else if eventName == common.EventTypes.Unsubscribe {
		err := e.deleteService(params.Org.GetOID())
		if err != nil {
			return common.Response{
				Error: fmt.Sprintf("failed to delete service: %v", err),
			}
		}
	}
	// Also always forward the event to the service.
	response, err := e.forwardEvent(ctx, eventName, params)
	if err != nil {
		return common.Response{
			Error: fmt.Sprintf("failed to forward event: %v", err),
		}
	}

	// Return the response.
	return *response
}

// In redis, the key is the service:OID and the value is a string of the Project ID, Region, and Service URL.
// Example: "service:{9f3888dd-ac17-4593-bd8c-efbcac12bfca} => 1234567890:us-central1:https://my_extension-9f3888dd-ac17-4593-bd8c-efbcac12bfca.a.run.app"
// We do this because there is a maximum number of Cloud Run services per project. So we might
// have to expand into new projects.

func parseServiceKeyValue(key string) (string, string, string) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	projectID := parts[0]
	region := parts[1]
	serviceURL := parts[2]
	return projectID, region, serviceURL
}

func serviceKey(serviceName string) string {
	return fmt.Sprintf("service:{%s}", serviceName)
}

func generateServiceKeyValue(projectID string, region string, serviceName string) string {
	return fmt.Sprintf("%s:%s:%s", projectID, region, serviceName)
}

func (e *CloudRunMultiplexer) generateServiceName(oid string) string {
	return fmt.Sprintf("%s-%s", e.ExtensionName, oid)
}

func (e *CloudRunMultiplexer) getService(oid string) (string, string, string, error) {
	key := serviceKey(oid)
	value, err := e.redisClient.Get(context.Background(), key).Result()
	if err != nil {
		return "", "", "", err
	}
	projectID, region, serviceURL := parseServiceKeyValue(value)
	return projectID, region, serviceURL, nil
}

func (e *CloudRunMultiplexer) createService(oid string) (string, string, error) {
	projectID := e.provisionProjectID
	region := e.provisionRegion
	serviceName := e.generateServiceName(oid)

	ctx := context.Background()

	// Create a new Cloud Run client
	runClient, err := run.NewServicesClient(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to create Cloud Run client: %v", err)
	}
	defer runClient.Close()

	// Prepare the service configuration
	service := &runpb.Service{
		Template: &runpb.RevisionTemplate{
			Containers: []*runpb.Container{
				{
					Image: e.serviceDefinition.Image,
					Resources: &runpb.ResourceRequirements{
						Limits: map[string]string{
							"cpu":    e.serviceDefinition.CPU,
							"memory": e.serviceDefinition.Memory,
						},
					},
					Env: make([]*runpb.EnvVar, len(e.serviceDefinition.Env)),
				},
			},
			Timeout: durationpb.New(time.Duration(e.serviceDefinition.Timeout) * time.Second),
			Scaling: &runpb.RevisionScaling{
				MinInstanceCount: e.serviceDefinition.MinInstances,
				MaxInstanceCount: e.serviceDefinition.MaxInstances,
			},
		},
	}

	// Convert environment variables
	for i, envStr := range e.serviceDefinition.Env {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) == 2 {
			service.Template.Containers[0].Env[i] = &runpb.EnvVar{
				Name: parts[0],
				Values: &runpb.EnvVar_Value{
					Value: parts[1],
				},
			}
		}
	}

	// Create the service
	req := &runpb.CreateServiceRequest{
		Parent:    fmt.Sprintf("projects/%s/locations/%s", projectID, region),
		ServiceId: serviceName,
		Service:   service,
	}

	op, err := runClient.CreateService(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("failed to create service: %v", err)
	}

	// Wait for the operation to complete
	resp, err := op.Wait(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to wait for service creation: %v", err)
	}

	// Store the service information in Redis
	key := serviceKey(oid)
	value := generateServiceKeyValue(projectID, region, serviceName)
	if err := e.redisClient.Set(ctx, key, value, 0).Err(); err != nil {
		// If we fail to store in Redis, try to clean up the service
		deleteReq := &runpb.DeleteServiceRequest{
			Name: resp.Name,
		}
		if _, err := runClient.DeleteService(ctx, deleteReq); err != nil {
			e.Error(fmt.Sprintf("failed to cleanup service after Redis error: %v", err))
		}
		return "", "", fmt.Errorf("failed to store service in Redis: %v", err)
	}

	return projectID, serviceName, nil
}

func (e *CloudRunMultiplexer) deleteService(oid string) error {
	projectID, region, _, err := e.getService(oid)
	if err != nil {
		return err
	}
	serviceName := e.generateServiceName(oid)

	ctx := context.Background()
	runClient, err := run.NewServicesClient(ctx)
	if err != nil {
		return err
	}
	defer runClient.Close()

	req := &runpb.DeleteServiceRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName),
	}

	_, err = runClient.DeleteService(ctx, req)
	return err
}

func (e *CloudRunMultiplexer) forwardRequest(ctx context.Context, action string, params core.RequestCallbackParams) (*common.Response, error) {
	_, _, serviceURL, err := e.getService(params.Org.GetOID())
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %v", err)
	}
	newReq := common.Message{
		Version:        core.PROTOCOL_VERSION,
		IdempotencyKey: params.IdempotentKey,
		Request: &common.RequestMessage{
			Org: common.OrgAccessData{
				OID: params.Org.GetOID(),
				JWT: params.Org.GetCurrentJWT(),
			},
			Action: action,
			Data:   params.Request.(limacharlie.Dict),
			Config: params.Config,
		},
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	return e.forwardHTTP(ctx, serviceURL, body)
}

func (e *CloudRunMultiplexer) forwardConfigValidation(ctx context.Context, org *limacharlie.Organization, serviceURL string, config limacharlie.Dict) (*common.Response, error) {
	newReq := common.Message{
		Version:        core.PROTOCOL_VERSION,
		IdempotencyKey: "",
		ConfigValidation: &common.ConfigValidationMessage{
			Org: common.OrgAccessData{
				OID: org.GetOID(),
				JWT: org.GetCurrentJWT(),
			},
			Config: config,
		},
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	return e.forwardHTTP(ctx, serviceURL, body)
}

func (e *CloudRunMultiplexer) forwardEvent(ctx context.Context, eventName common.EventName, params core.EventCallbackParams) (*common.Response, error) {
	_, _, serviceURL, err := e.getService(params.Org.GetOID())
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %v", err)
	}
	newReq := common.Message{
		Version:        core.PROTOCOL_VERSION,
		IdempotencyKey: params.IdempotentKey,
		Event: &common.EventMessage{
			Org: common.OrgAccessData{
				OID: params.Org.GetOID(),
				JWT: params.Org.GetCurrentJWT(),
			},
			EventName: eventName,
			Data:      params.Data,
			Config:    params.Conf,
		},
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	return e.forwardHTTP(ctx, serviceURL, body)
}

func (e *CloudRunMultiplexer) forwardHTTP(ctx context.Context, serviceURL string, body []byte) (*common.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serviceURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http.NewRequestWithContext: %v", err)
	}
	httpReq.Header.Set("lc-ext-sig", e.signForSelf(body))

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http.Do: %v", err)
	}
	defer resp.Body.Close()

	response := &common.Response{}
	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return response, nil
}

func (e *CloudRunMultiplexer) signForSelf(data []byte) string {
	mac := hmac.New(sha256.New, []byte(e.SecretKey))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
