package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
	"github.com/refractionPOINT/lc-extension/server/webserver"

	iampb "cloud.google.com/go/iam/apiv1/iampb"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"github.com/go-redis/redis/v8"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This is the definition of a Cloud Run service
// we can use to create a new service.
type CloudRunServiceDefinition struct {
	Image          string   `json:"image"`
	Env            []string `json:"env"`
	CPU            string   `json:"cpu"`
	Memory         string   `json:"memory"`
	MinInstances   int32    `json:"min_instances"`
	MaxInstances   int32    `json:"max_instances"`
	Timeout        int32    `json:"timeout"`
	ServiceAccount string   `json:"service_account"`
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

	// The shared secret that the worker will use to authenticate with the multiplexer.
	workerSharedSecret string
}

var Extension *CloudRunMultiplexer

func main() {
	// Because this will be configured entirely through environment variables,
	// we will parse the configuration from making a request to a reference
	// service as defined by a LC_REFERENCE_SERVICE_URL environment variable.

	rs := os.Getenv("LC_REFERENCE_SERVICE_URL")
	if rs == "" {
		panic("LC_REFERENCE_SERVICE_URL is not set")
	}

	ws := os.Getenv("LC_WORKER_SHARED_SECRET")
	if ws == "" {
		panic("LC_WORKER_SHARED_SECRET is not set")
	}

	en := os.Getenv("LC_EXTENSION_NAME")
	if en == "" {
		panic("LC_EXTENSION_NAME is not set")
	}

	secret := os.Getenv("LC_SHARED_SECRET")
	if secret == "" {
		panic("LC_SHARED_SECRET is not set")
	}

	provProjectID := os.Getenv("PROVISION_PROJECT_ID")
	if provProjectID == "" {
		panic("PROVISION_PROJECT_ID is not set")
	}
	provRegion := os.Getenv("PROVISION_REGION")
	if provRegion == "" {
		panic("PROVISION_REGION is not set")
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		panic("REDIS_ADDR is not set")
	}

	// Make a request to the reference service to get the configuration by getting
	// a SchemaRequest.
	sr := common.SchemaRequestMessage{}
	sc := &http.Client{
		Timeout: 10 * time.Minute,
	}
	ss, err := json.Marshal(sr)
	if err != nil {
		panic(err)
	}
	resp, err := forwardHTTP(context.Background(), []byte(ws), sc, rs, ss)
	if err != nil {
		panic(err)
	}
	if resp.Error != "" {
		panic(resp.Error)
	}
	// The data is a SchemaRequestResponse in JSON form, so
	// serialize and deserialize it into the struct.
	ss, err = json.Marshal(resp.Data)
	if err != nil {
		panic(err)
	}
	srResp := common.SchemaRequestResponse{}
	if err := json.Unmarshal(ss, &srResp); err != nil {
		panic(err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
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
			SecretKey:      secret,
			ConfigSchema:   srResp.Config,
			RequestSchema:  srResp.Request,
			ViewsSchema:    srResp.Views,
			RequiredEvents: srResp.RequiredEvents,
		},
		limacharlie.LCLoggerGCP{},
		redisClient,
		provProjectID,
		provRegion,
		serviceDefinition,
		&http.Client{
			Timeout: 10 * time.Minute,
		},
		ws,
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
		return "", "", fmt.Errorf("NewServicesClient(): %v", err)
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
					Env: make([]*runpb.EnvVar, len(e.serviceDefinition.Env)+1),
				},
			},
			Timeout: durationpb.New(time.Duration(e.serviceDefinition.Timeout) * time.Second),
			Scaling: &runpb.RevisionScaling{
				MinInstanceCount: e.serviceDefinition.MinInstances,
				MaxInstanceCount: e.serviceDefinition.MaxInstances,
			},
			ServiceAccount: e.serviceDefinition.ServiceAccount,
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
	service.Template.Containers[0].Env[len(e.serviceDefinition.Env)] = &runpb.EnvVar{
		Name: "FROM_LC_OID",
		Values: &runpb.EnvVar_Value{
			Value: oid,
		},
	}

	// Create the service
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	req := &runpb.CreateServiceRequest{
		Parent:    parent,
		ServiceId: serviceName,
		Service:   service,
	}

	op, err := runClient.CreateService(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("CreateService(): %v", err)
	}

	// Wait for the operation to complete
	resp, err := op.Wait(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to wait for service creation: %v", err)
	}

	// Store the service information in Redis
	key := serviceKey(oid)
	value := generateServiceKeyValue(projectID, region, resp.Uri)
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

	// Make the service public.
	if err := makeServicePublic(ctx, runClient, fmt.Sprintf("%s/services/%s", parent, serviceName)); err != nil {
		return "", "", fmt.Errorf("failed to make service public: %v", err)
	}

	return projectID, serviceName, nil
}

func makeServicePublic(ctx context.Context, client *run.ServicesClient, serviceName string) error {
	// Get the current IAM policy
	getPolicyReq := &iampb.GetIamPolicyRequest{
		Resource: serviceName,
	}
	policy, err := client.GetIamPolicy(ctx, getPolicyReq)
	if err != nil {
		return fmt.Errorf("failed to get IAM policy: %v", err)
	}

	// Modify the policy to add roles/run.invoker for allUsers
	binding := &iampb.Binding{
		Role:    "roles/run.invoker",
		Members: []string{"allUsers"},
	}
	policy.Bindings = append(policy.Bindings, binding)

	// Set the updated IAM policy
	setPolicyReq := &iampb.SetIamPolicyRequest{
		Resource: serviceName,
		Policy:   policy,
	}
	_, err = client.SetIamPolicy(ctx, setPolicyReq)
	if err != nil {
		return fmt.Errorf("failed to set IAM policy: %v", err)
	}

	return nil
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
	return forwardHTTP(ctx, []byte(e.workerSharedSecret), e.httpClient, serviceURL, body)
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
	return forwardHTTP(ctx, []byte(e.workerSharedSecret), e.httpClient, serviceURL, body)
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
	return forwardHTTP(ctx, []byte(e.workerSharedSecret), e.httpClient, serviceURL, body)
}

func forwardHTTP(ctx context.Context, secret []byte, client *http.Client, serviceURL string, body []byte) (*common.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serviceURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http.NewRequestWithContext: %v", err)
	}
	httpReq.Header.Set("lc-ext-sig", signForSelf(secret, body))

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http.Do: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("io.ReadAll: %v", err)
	}

	response := &common.Response{}
	if err := json.Unmarshal(bodyBytes, response); err != nil {
		return nil, fmt.Errorf("json.Unmarshal: %v (received: %s)", err, string(bodyBytes))
	}

	return response, nil
}

func signForSelf(secret []byte, data []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
