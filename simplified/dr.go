package simplified

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

type GetRulesCallback = func(ctx context.Context) (RuleData, error)
type RuleName = string
type RuleNamespace = string
type RuleInfo struct {
	Tags []string
	Data limacharlie.Dict
}
type RuleData = map[RuleNamespace]map[RuleName]RuleInfo

type RuleExtension struct {
	Name      string
	SecretKey string
	Logger    limacharlie.LCLogger

	GetRules GetRulesCallback

	tag      string
	ruleName string
}

type ruleUpdateRequest struct{}

type ruleConfig struct {
	DisableByDefault      bool   `json:"disable_by_default"`
	GlobalSuppressionTime string `json:"global_suppression_time"`
}

var simplifiedRuleNamespaces = map[string]struct{}{
	"general": {},
	"managed": {},
	"service": {},
}

func (l *RuleExtension) Init() (*core.Extension, error) {
	l.tag = fmt.Sprintf("ext:%s", l.Name)
	l.ruleName = fmt.Sprintf("ext-%s-update", l.Name)

	x := &core.Extension{
		ExtensionName: l.Name,
		SecretKey:     l.SecretKey,
		// The schema defining what the configuration for this Extension should look like.
		ConfigSchema: common.SchemaObject{
			Fields: map[common.SchemaKey]common.SchemaElement{
				"disable_by_default": {
					DataType:     common.SchemaDataTypes.Boolean,
					Description:  "disable new rules by default after the initial subscription",
					DefaultValue: false,
					Label:        "Disable new rules by default",
				},
				"global_suppression_time": {
					DataType:     common.SchemaDataTypes.String,
					Description:  "global suppression period for all detections for rules created by this extension",
					DefaultValue: "",
					Label:        "Global suppression time",
					PlaceHolder:  "24h",
				},
			},
			Requirements: [][]common.SchemaKey{},
		},
		// The schema defining what requests to this Extension should look like.
		RequestSchema: map[string]common.RequestSchema{
			"update_rules": {
				IsUserFacing:         false,
				ShortDescription:     "update the rules",
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
			"update_rules": {
				RequestStruct: &ruleUpdateRequest{},
				Callback:      l.onUpdate,
			},
		},
		EventHandlers: map[common.EventName]core.EventCallback{
			common.EventTypes.Subscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf limacharlie.Dict, idempotentKey string) common.Response {
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
							"extension action":  "update_rules",
							"extension request": limacharlie.Dict{},
						}},
					},
					Tags:    []string{l.tag},
					Enabled: &trueValue,
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to add scheduling D&R rule: %s", err.Error()))
					return common.Response{Error: err.Error()}
				}

				// We also push the initial update.
				resp := l.onInstall(ctx, org, nil, nil, "")
				if resp.Error != "" {
					return resp
				}

				return common.Response{}
			},
			// An Org unsubscribed.
			common.EventTypes.Unsubscribe: func(ctx context.Context, org *limacharlie.Organization, data, conf limacharlie.Dict, idempotentKey string) common.Response {
				l.Logger.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))

				// Remove the D&R rule we set up.
				h := limacharlie.NewHiveClient(org)
				if _, err := h.Remove(limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to remove scheduling D&R rule: %s", err.Error()))
					return common.Response{Error: err.Error()}
				}

				// For every namespace, remove rules with matching tags.
				for namespace := range simplifiedRuleNamespaces {
					namespace = fmt.Sprintf("dr-%s", namespace)
					rules, err := h.ListMtd(limacharlie.HiveArgs{
						HiveName:     namespace,
						PartitionKey: org.GetOID(),
					})
					if err != nil {
						l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
						return common.Response{Error: err.Error()}
					}
					for ruleName, ruleData := range rules {
						isRemove := false
						for _, t := range ruleData.UsrMtd.Tags {
							if t == l.tag {
								isRemove = true
								break
							}
						}
						if !isRemove {
							continue
						}
						if _, err := h.Remove(limacharlie.HiveArgs{
							HiveName:     namespace,
							PartitionKey: org.GetOID(),
							Key:          ruleName,
						}); err != nil {
							l.Logger.Error(fmt.Sprintf("failed to remove rule %s: %s", ruleName, err.Error()))
							return common.Response{Error: err.Error()}
						}
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

func (l *RuleExtension) onUpdate(ctx context.Context, org *limacharlie.Organization, data interface{}, conf limacharlie.Dict, idempotentKey string) common.Response {
	h := limacharlie.NewHiveClient(org)

	config := ruleConfig{}
	if err := conf.UnMarshalToStruct(&config); err != nil {
		return common.Response{Error: err.Error()}
	}

	wg := sync.WaitGroup{}
	rulesData, err := l.GetRules(ctx)
	if err != nil {
		return common.Response{Error: err.Error()}
	}

	sm := semaphore.NewWeighted(100)

	for namespace, rules := range rulesData {
		if _, ok := simplifiedRuleNamespaces[namespace]; !ok {
			l.Logger.Error(fmt.Sprintf("invalid namespace %s", namespace))
			continue
		}
		namespace = fmt.Sprintf("dr-%s", namespace)
		for ruleName, ruleData := range rules {
			ruleName, ruleData := ruleName, ruleData
			if err := sm.Acquire(ctx, 1); err != nil {
				return common.Response{Error: err.Error()}
			}
			l.Logger.Info(fmt.Sprintf("updating rule %s", ruleName))
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer sm.Release(1)

				// We need to do a transactional update to check if the
				// rule exists before we set it.
				if _, err := h.UpdateTx(limacharlie.HiveArgs{
					HiveName:     namespace,
					PartitionKey: org.GetOID(),
					Key:          ruleName,
				}, func(record *limacharlie.HiveData) (*limacharlie.HiveData, error) {
					// If the rule does not exist (null), just add
					// it with the enable by default flag.
					if record == nil {
						return &limacharlie.HiveData{
							Data: ruleData.Data,
							UsrMtd: limacharlie.UsrMtd{
								Enabled: !config.DisableByDefault,
								Tags:    l.mergeTags(ruleData.Tags, []string{}),
							},
						}, nil
					}
					// If the rule exists, only update its data and upsert
					// its tags. That way if the user disabled it or tagged
					// it, we leave it that way.
					record.Data = ruleData.Data
					record.UsrMtd.Tags = l.mergeTags(record.UsrMtd.Tags, ruleData.Tags)
					return record, nil
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to update rule %s: %s", ruleName, err.Error()))
				}
			}()
		}

		// Get the list of rules in prod and check they're all in our local list.
		// If not, we'll remove them.
		wg.Add(1)
		go func() {
			defer wg.Done()
			existingRules, err := h.ListMtd(limacharlie.HiveArgs{
				HiveName:     namespace,
				PartitionKey: org.GetOID(),
			})
			if err != nil {
				l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
				return
			}
			for ruleName := range existingRules {
				if _, ok := rules[ruleName]; ok {
					continue
				}
				ruleName := ruleName
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := h.Remove(limacharlie.HiveArgs{
						HiveName:     namespace,
						PartitionKey: org.GetOID(),
						Key:          ruleName,
					}); err != nil {
						l.Logger.Error(fmt.Sprintf("failed to remove rule %s: %s", ruleName, err.Error()))
					}
				}()
			}
		}()
	}

	wg.Wait()

	l.Logger.Info("done updating rules")

	return common.Response{}
}

func (l *RuleExtension) onInstall(ctx context.Context, org *limacharlie.Organization, data interface{}, conf limacharlie.Dict, idempotentKey string) common.Response {
	h := limacharlie.NewHiveClient(org)

	config := ruleConfig{}
	if err := conf.UnMarshalToStruct(&config); err != nil {
		return common.Response{Error: err.Error()}
	}

	wg := sync.WaitGroup{}
	rulesData, err := l.GetRules(ctx)
	if err != nil {
		return common.Response{Error: err.Error()}
	}

	sm := semaphore.NewWeighted(100)
	trueValue := true

	for namespace, rules := range rulesData {
		if _, ok := simplifiedRuleNamespaces[namespace]; !ok {
			l.Logger.Error(fmt.Sprintf("invalid namespace %s", namespace))
			continue
		}
		namespace = fmt.Sprintf("dr-%s", namespace)
		for ruleName, ruleData := range rules {
			ruleName, ruleData := ruleName, ruleData
			if err := sm.Acquire(ctx, 1); err != nil {
				return common.Response{Error: err.Error()}
			}
			l.Logger.Info(fmt.Sprintf("installing rule %s", ruleName))
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer sm.Release(1)

				// On Install we just add the rule with the enable by default flag.
				if _, err := h.Add(limacharlie.HiveArgs{
					HiveName:     namespace,
					PartitionKey: org.GetOID(),
					Key:          ruleName,
					Data:         ruleData.Data,
					Enabled:      &trueValue,
					Tags:         l.mergeTags(ruleData.Tags, []string{}),
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to add rule %s: %s", ruleName, err.Error()))
				}
			}()
		}
	}
}

func (l *RuleExtension) mergeTags(t1 []string, t2 []string) []string {
	tags := map[string]struct{}{
		l.tag: {}, // Prime with our core tag.
	}
	for _, t := range t1 {
		tags[t] = struct{}{}
	}
	for _, t := range t2 {
		tags[t] = struct{}{}
	}
	res := []string{}
	for t := range tags {
		res = append(res, t)
	}
	return res
}
