package simplified

import (
	"context"
	"fmt"
	"sync"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

type GetRulesCallback = func(ctx context.Context) (RuleData, error)
type RuleName = string
type RuleNamespace = string
type RuleData = map[RuleNamespace]map[RuleName]limacharlie.Dict

type RuleExtension struct {
	Name      string
	SecretKey string
	Logger    limacharlie.LCLogger

	GetRules GetRulesCallback

	tag      string
	ruleName string
}

type ruleUpdateRequest struct{}

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
			Fields:       map[common.SchemaKey]common.SchemaElement{},
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
				resp := l.onUpdate(ctx, org, nil, nil, "")
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

	wg := sync.WaitGroup{}
	rulesData, err := l.GetRules(ctx)
	if err != nil {
		return common.Response{Error: err.Error()}
	}
	for namespace, rules := range rulesData {
		if _, ok := simplifiedRuleNamespaces[namespace]; !ok {
			l.Logger.Error(fmt.Sprintf("invalid namespace %s", namespace))
			continue
		}
		namespace = fmt.Sprintf("dr-%s", namespace)
		for ruleName, ruleData := range rules {
			ruleName, ruleData := ruleName, ruleData
			l.Logger.Info(fmt.Sprintf("updating rule %s", ruleName))
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Push the update.
				if _, err := h.Add(limacharlie.HiveArgs{
					HiveName:     namespace,
					PartitionKey: org.GetOID(),
					Key:          ruleName,
					Data:         ruleData,
					Tags:         []string{l.tag},
				}); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to update rule %s: %s", ruleName, err.Error()))
					return
				}
			}()
		}
	}

	wg.Wait()

	l.Logger.Info("done updating rules")

	return common.Response{}
}
