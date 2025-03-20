package simplified

import (
	"context"
	"fmt"
	"sync"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

const updateRuleHive = "dr-managed"

type (
	GetLookupCallback = func(ctx context.Context) (LookupData, error)
	LookupName        = string
	LookupData        = map[LookupName]interface{}
)

type LookupExtension struct {
	Name      string
	SecretKey string
	Logger    limacharlie.LCLogger

	GetLookup     GetLookupCallback
	EventHandlers map[common.EventName]core.EventCallback // Optional

	tag      string
	ruleName string
}

type lookupUpdateRequest struct{}

func (l *LookupExtension) Init() (*core.Extension, error) {
	l.tag = fmt.Sprintf("ext:%s", l.Name)
	l.ruleName = fmt.Sprintf("ext-%s-update", l.Name)

	x := &core.Extension{
		ExtensionName: l.Name,
		SecretKey:     l.SecretKey,
		RequiredEvents: []common.EventName{
			common.EventTypes.Subscribe,
			common.EventTypes.Unsubscribe,
			common.EventTypes.Update,
		},
		// The schema defining what the configuration for this Extension should look like.
		ConfigSchema: common.SchemaObject{
			Fields:       map[common.SchemaKey]common.SchemaElement{},
			Requirements: [][]common.SchemaKey{},
		},
		// The schema defining what requests to this Extension should look like.
		RequestSchema: map[string]common.RequestSchema{
			"update_lookup": {
				IsUserFacing:         false,
				ShortDescription:     "update the lookup",
				IsImpersonated:       false,
				ParameterDefinitions: common.SchemaObject{},
				ResponseDefinition:   &common.SchemaObject{},
			},
		},
	}

	x.Callbacks = core.ExtensionCallbacks{
		ValidateConfig: func(ctx context.Context, org *limacharlie.Organization, config limacharlie.Dict) common.Response {
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"update_lookup": {
				RequestStruct: &lookupUpdateRequest{},
				Callback:      l.onUpdate,
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				org := params.Org
				l.Logger.Info(fmt.Sprintf("subscribe to %s", org.GetOID()))

				// We set up a D&R rule for recurring update.
				h := limacharlie.NewHiveClient(org)
				trueValue := true
				if _, err := h.Add(limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
					Data: limacharlie.Dict{
						"detect": limacharlie.Dict{
							"target": "schedule",
							"event":  "12h_per_org",
							"op":     "exists",
							"path":   "event",
						},
						"respond": []limacharlie.Dict{{
							"action":            "extension request",
							"extension name":    l.Name,
							"extension action":  "update_lookup",
							"extension request": limacharlie.Dict{},
						}},
					},
					Tags:    []string{l.tag},
					Enabled: &trueValue,
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to add D&R rule: %s", err.Error()))
					return common.Response{Error: err.Error()}
				}

				if h, ok := l.EventHandlers[common.EventTypes.Subscribe]; ok {
					if resp := h(ctx, params); resp.Error != "" {
						return resp
					}
				}

				// The initial update will be done asynchronously.
				return common.Response{Continuations: []common.ContinuationRequest{{
					InDelaySeconds: 1,
					Action:         "update_lookup",
					State:          limacharlie.Dict{},
				}}}
			},
			// An Org unsubscribed.
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				org := params.Org
				l.Logger.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))

				// Remove the D&R rule we set up.
				h := limacharlie.NewHiveClient(org)
				if _, err := h.Remove(limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to remove D&R rule: %s", err.Error()))
					return common.Response{Error: err.Error()}
				}

				// We also remove the lookups.
				lookups, err := h.ListMtd(limacharlie.HiveArgs{
					HiveName:     "lookup",
					PartitionKey: org.GetOID(),
				})
				if err != nil {
					l.Logger.Error(fmt.Sprintf("failed to list lookups: %s", err.Error()))
					return common.Response{Error: err.Error()}
				}
				for luName, luData := range lookups {
					isRemove := false
					for _, t := range luData.UsrMtd.Tags {
						if t == l.tag {
							isRemove = true
							break
						}
					}
					if !isRemove {
						continue
					}
					if _, err := h.Remove(limacharlie.HiveArgs{
						HiveName:     "lookup",
						PartitionKey: org.GetOID(),
						Key:          luName,
					}); err != nil {
						l.Logger.Error(fmt.Sprintf("failed to remove lookup %s: %s", luName, err.Error()))
						return common.Response{Error: err.Error()}
					}
				}

				if h, ok := l.EventHandlers[common.EventTypes.Unsubscribe]; ok {
					if resp := h(ctx, params); resp.Error != "" {
						return resp
					}
				}

				return common.Response{}
			},
			common.EventTypes.Update: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				if h, ok := l.EventHandlers[common.EventTypes.Update]; ok {
					if resp := h(ctx, params); resp.Error != "" {
						return resp
					}
				}
				return common.Response{}
			},
		},
		ErrorHandler: func(errMsg *common.ErrorReportMessage) {
			l.Logger.Error(fmt.Sprintf("error from limacharlie: %s", errMsg.Error))
		},
	}
	// Start processing.
	if err := x.Init(); err != nil {
		panic(err)
	}

	return x, nil
}

func (l *LookupExtension) onUpdate(ctx context.Context, params core.RequestCallbackParams) common.Response {
	h := limacharlie.NewHiveClient(params.Org)

	wg := sync.WaitGroup{}
	lookups, err := l.GetLookup(ctx)
	if err != nil {
		return common.Response{Error: err.Error()}
	}
	for luName, luData := range lookups {
		luName, luData := luName, luData
		l.Logger.Info(fmt.Sprintf("updating lookup %s", luName))
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Convert the interface to a Dict.
			d := limacharlie.Dict{}
			if _, err := d.ImportFromStruct(luData); err != nil {
				l.Logger.Error(fmt.Sprintf("failed to unmarshal lookup %s: %s", luName, err.Error()))
				return
			}

			// Push the update.
			isTrue := true
			if _, err := h.Add(limacharlie.HiveArgs{
				HiveName:     "lookup",
				PartitionKey: params.Org.GetOID(),
				Key:          luName,
				Data: limacharlie.Dict{
					"lookup_data": d,
				},
				Tags:    []string{l.tag},
				Enabled: &isTrue,
			}); err != nil {
				l.Logger.Error(fmt.Sprintf("failed to update lookup %s: %s", luName, err.Error()))
				return
			}
		}()
	}

	wg.Wait()

	l.Logger.Info("done updating lookups")

	return common.Response{}
}
