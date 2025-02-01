package multiplexer

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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"

	"cloud.google.com/go/datastore"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

const serviceCacheTTL = 10 * time.Second

// This is the definition of a Cloud Run service
// we can use to create a new service.
type CloudRunServiceDefinition struct {
	Image          string            `json:"image"`
	Env            []string          `json:"env"`
	CPU            string            `json:"cpu"`
	Memory         string            `json:"memory"`
	MinInstances   int32             `json:"min_instances"`
	MaxInstances   int32             `json:"max_instances"`
	Timeout        int32             `json:"timeout"`
	ServiceAccount string            `json:"service_account"`
	Labels         map[string]string `json:"labels"`
}

type Multiplexer struct {
	core.Extension
	limacharlie.LCLoggerGCP

	// Datastore client used to store and retrieve service information.
	datastoreClient *datastore.Client

	// The project ID where we will provision new Cloud Run services.
	provisionProjectID string
	provisionRegion    string

	// The definition of the Cloud Run service we will use to create new services.
	serviceDefinition CloudRunServiceDefinition

	// HTTP Client to use to forward requests to the service.
	httpClient *http.Client

	// The shared secret that the worker will use to authenticate with the multiplexer.
	refereceSharedSecret string

	// Cache the service definitions in memory.
	serviceCache      map[string]ServiceDefinition
	serviceCacheMutex sync.RWMutex
	lastServiceUpdate time.Time

	// Optional hooks for users that want to do custom processing.
	HookSendMessage func(ctx context.Context, ext *Multiplexer, org *limacharlie.Organization, message *common.Message) (*common.Message, error)
	HookResponse    func(ctx context.Context, ext *Multiplexer, org *limacharlie.Organization, message *common.Message, response *common.Response, startTime time.Time) (*common.Response, error)
}

type ServiceDefinition struct {
	ProjectID string `json:"project_id" datastore:"project_id"`
	Region    string `json:"region" datastore:"region"`
	URL       string `json:"url" datastore:"url"`
	Secret    string `json:"secret" datastore:"secret"`
}

var Extension *Multiplexer

func init() {
	// Because this will be configured entirely through environment variables,
	// we will parse the configuration from making a request to a reference
	// service as defined by a LC_REFERENCE_SERVICE_URL environment variable.
	// The worker service must use the LC_SHARED_SECRET environment variable
	// as a shared secret to authenticate with the multiplexer.

	rs := os.Getenv("LC_REFERENCE_SERVICE_URL")
	if rs == "" {
		panic("LC_REFERENCE_SERVICE_URL is not set")
	}

	ws := os.Getenv("LC_REFERENCE_SHARED_SECRET")
	if ws == "" {
		panic("LC_REFERENCE_SHARED_SECRET is not set")
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

	dsClient, err := datastore.NewClient(context.Background(), provProjectID)
	if err != nil {
		panic(err)
	}

	// Make a request to the reference service to get the configuration by getting
	// a SchemaRequest.
	req := common.Message{
		Version:       core.PROTOCOL_VERSION,
		SchemaRequest: &common.SchemaRequestMessage{},
	}
	sc := &http.Client{
		Timeout: 10 * time.Minute,
	}
	ss, err := json.Marshal(&req)
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

	serviceDefinition := CloudRunServiceDefinition{}
	if err := json.Unmarshal([]byte(os.Getenv("SERVICE_DEFINITION")), &serviceDefinition); err != nil {
		panic(err)
	}
	Extension = &Multiplexer{
		core.Extension{
			ExtensionName:  en,
			SecretKey:      secret,
			ConfigSchema:   srResp.Config,
			RequestSchema:  srResp.Request,
			ViewsSchema:    srResp.Views,
			RequiredEvents: srResp.RequiredEvents,
		},
		limacharlie.LCLoggerGCP{},
		dsClient,
		provProjectID,
		provRegion,
		serviceDefinition,
		&http.Client{
			Timeout: 10 * time.Minute,
		},
		ws,
		make(map[string]ServiceDefinition),
		sync.RWMutex{},
		time.Time{},
		nil,
		nil,
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
			_, _, serviceURL, secret, err := Extension.getService(org.GetOID())
			if err != nil {
				return common.Response{
					Error: fmt.Sprintf("failed to get service: %v", err),
				}
			}

			// Forward the request.
			response, err := Extension.forwardConfigValidation(ctx, org, serviceURL, secret, config)
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
}

func (e *Multiplexer) Init() error {
	// Initialize the Extension core.
	if err := e.Extension.Init(); err != nil {
		return err
	}

	return nil
}

func (e *Multiplexer) OnGenericRequest(ctx context.Context, actionName common.ActionName, params core.RequestCallbackParams) common.Response {
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

func (e *Multiplexer) OnGenericEvent(ctx context.Context, eventName common.EventName, params core.EventCallbackParams) common.Response {
	// If it is a subscribe event, we need to create a new service.
	// If it is an unsubscribe event, we need to delete the service.
	if eventName == common.EventTypes.Subscribe {
		_, _, err := e.createService(params.Org.GetOID())
		if err != nil {
			return common.Response{
				Error: fmt.Sprintf("failed to create service: %v", err),
			}
		}
	}
	// Also always forward the event to the service.
	response, err := e.forwardEvent(ctx, eventName, params)
	if err != nil {
		if eventName == common.EventTypes.Subscribe {
			// There was an error creating the service, so we need to delete it.
			e.deleteService(params.Org.GetOID())
		}
		return common.Response{
			Error: fmt.Sprintf("failed to forward event: %v", err),
		}
	}

	if eventName == common.EventTypes.Unsubscribe {
		err := e.deleteService(params.Org.GetOID())
		if err != nil {
			return common.Response{
				Error: fmt.Sprintf("failed to delete service: %v", err),
			}
		}
	}

	// Return the response.
	return *response
}

// In Datastore, the key is the OID and the values are the Project ID, Region, and Service URL.
// Example: "9f3888dd-ac17-4593-bd8c-efbcac12bfca => 1234567890:us-central1:https://my_extension-9f3888dd-ac17-4593-bd8c-efbcac12bfca.a.run.app"
// We do this because there is a maximum number of Cloud Run services per project. So we might
// have to expand into new projects.

func (e *Multiplexer) generateServiceName(oid string) string {
	return fmt.Sprintf("%s-%s", e.ExtensionName, oid)
}

func (e *Multiplexer) getService(oid string) (string, string, string, string, error) {
	e.serviceCacheMutex.RLock()
	def, ok := e.serviceCache[oid]
	lastServiceUpdate := e.lastServiceUpdate
	e.serviceCacheMutex.RUnlock()
	if lastServiceUpdate.Add(serviceCacheTTL).Before(time.Now()) {
		// We need to refresh the cache.
		e.serviceCacheMutex.Lock()
		e.serviceCache = make(map[string]ServiceDefinition)
		e.lastServiceUpdate = time.Time{}
		e.serviceCacheMutex.Unlock()
		ok = false
	}
	if ok {
		return def.ProjectID, def.Region, def.URL, def.Secret, nil
	}

	if err := e.datastoreClient.Get(context.Background(), datastore.NameKey("service", oid, nil), &def); err != nil {
		return "", "", "", "", fmt.Errorf("failed to get service: %v", err)
	}
	e.serviceCacheMutex.Lock()
	e.serviceCache[oid] = def
	e.serviceCacheMutex.Unlock()
	return def.ProjectID, def.Region, def.URL, def.Secret, nil
}

func (e *Multiplexer) createService(oid string) (string, string, error) {
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

	newSecret := uuid.New().String()

	// Add some a base label for the tenant.
	labels := map[string]string{
		"lc-oid":       oid,
		"lc-extension": e.ExtensionName,
	}
	// Combine with the static labels.
	for k, v := range e.serviceDefinition.Labels {
		labels[k] = v
	}

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
		Labels: labels,
	}

	// Convert environment variables
	for i, envStr := range e.serviceDefinition.Env {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) == 2 {
			// Look for the LC_SHARED_SECRET and replace it with the secret from the dynamic secret.
			if parts[0] == "LC_SHARED_SECRET" {
				parts[1] = newSecret
			}
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

	// Store the service information in Datastore
	if _, err := e.datastoreClient.Put(ctx, datastore.NameKey("service", oid, nil), &ServiceDefinition{
		ProjectID: projectID,
		Region:    region,
		URL:       resp.Uri,
		Secret:    newSecret,
	}); err != nil {
		// If we fail to store in Datastore, try to clean up the service
		deleteReq := &runpb.DeleteServiceRequest{
			Name: resp.Name,
		}
		if _, err := runClient.DeleteService(ctx, deleteReq); err != nil {
			e.Error(fmt.Sprintf("failed to cleanup service after Datastore error: %v", err))
		}
		return "", "", fmt.Errorf("failed to store service in Datastore: %v", err)
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

func (e *Multiplexer) deleteService(oid string) error {
	projectID, region, _, _, err := e.getService(oid)
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
	if err != nil {
		return fmt.Errorf("failed to delete service: %v", err)
	}

	if err := e.datastoreClient.Delete(ctx, datastore.NameKey("service", oid, nil)); err != nil {
		return fmt.Errorf("failed to delete service from Datastore: %v", err)
	}

	e.serviceCacheMutex.Lock()
	delete(e.serviceCache, oid)
	e.serviceCacheMutex.Unlock()

	return nil
}

func (e *Multiplexer) forwardRequest(ctx context.Context, action string, params core.RequestCallbackParams) (*common.Response, error) {
	_, _, serviceURL, secret, err := e.getService(params.Org.GetOID())
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %v", err)
	}
	newReq := &common.Message{
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
	if e.HookSendMessage != nil {
		newReq, err = e.HookSendMessage(ctx, e, params.Org, newReq)
		if err != nil {
			return nil, fmt.Errorf("HookSendMessage: %v", err)
		}
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	startTime := time.Now()
	response, err := forwardHTTP(ctx, []byte(secret), e.httpClient, serviceURL, body)
	if err != nil {
		return nil, fmt.Errorf("forwardHTTP: %v", err)
	}
	if e.HookResponse != nil {
		response, err = e.HookResponse(ctx, e, params.Org, newReq, response, startTime)
		if err != nil {
			return nil, fmt.Errorf("HookResponse: %v", err)
		}
	}
	return response, nil
}

func (e *Multiplexer) forwardConfigValidation(ctx context.Context, org *limacharlie.Organization, serviceURL string, secret string, config limacharlie.Dict) (*common.Response, error) {
	newReq := &common.Message{
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
	if e.HookSendMessage != nil {
		var err error
		newReq, err = e.HookSendMessage(ctx, e, org, newReq)
		if err != nil {
			return nil, fmt.Errorf("HookSendMessage: %v", err)
		}
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	response, err := forwardHTTP(ctx, []byte(secret), e.httpClient, serviceURL, body)
	if err != nil {
		return nil, fmt.Errorf("forwardHTTP: %v", err)
	}
	if e.HookResponse != nil {
		response, err = e.HookResponse(ctx, e, org, newReq, response)
		if err != nil {
			return nil, fmt.Errorf("HookResponse: %v", err)
		}
	}
	return response, nil
}

func (e *Multiplexer) forwardEvent(ctx context.Context, eventName common.EventName, params core.EventCallbackParams) (*common.Response, error) {
	_, _, serviceURL, secret, err := e.getService(params.Org.GetOID())
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %v", err)
	}
	newReq := &common.Message{
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
	if e.HookSendMessage != nil {
		newReq, err = e.HookSendMessage(ctx, e, params.Org, newReq)
		if err != nil {
			return nil, fmt.Errorf("HookSendMessage: %v", err)
		}
	}
	body, err := json.Marshal(newReq)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}
	response, err := forwardHTTP(ctx, []byte(secret), e.httpClient, serviceURL, body)
	if err != nil {
		return nil, fmt.Errorf("forwardHTTP: %v", err)
	}
	if e.HookResponse != nil {
		response, err = e.HookResponse(ctx, e, params.Org, newReq, response)
		if err != nil {
			return nil, fmt.Errorf("HookResponse: %v", err)
		}
	}
	return response, nil
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
