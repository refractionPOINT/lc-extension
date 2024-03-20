package simplified

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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

	GetRules      GetRulesCallback
	EventHandlers map[common.EventName]core.EventCallback // Optional

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

var isDebugLogRules = os.Getenv("DEBUG_LOG_RULES") != ""

func (l *RuleExtension) Init() (*core.Extension, error) {
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
			Fields: map[common.SchemaKey]common.SchemaElement{
				"disable_by_default": {
					DataType:     common.SchemaDataTypes.Boolean,
					Description:  "disable new rules by default after the initial subscription",
					DefaultValue: false,
					Label:        "Disable new rules by default",
				},
				"global_suppression_time": {
					DataType:     common.SchemaDataTypes.String,
					Description:  "global suppression period for all detections for rules created by this extension like \"30m\" or \"1h\", with a max of \"24h\".",
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
			c := ruleConfig{}
			if err := config.UnMarshalToStruct(&c); err != nil {
				return common.Response{Error: err.Error()}
			}
			if c.GlobalSuppressionTime != "" {
				d, err := time.ParseDuration(c.GlobalSuppressionTime)
				if err != nil {
					return common.Response{Error: fmt.Sprintf("invalid global suppression time: %s", err.Error())}
				}
				if d > 24*time.Hour {
					return common.Response{Error: "global suppression time cannot be more than 24h"}
				}
			}
			return common.Response{}
		},
		RequestHandlers: map[common.ActionName]core.RequestCallback{
			"update_rules": {
				RequestStruct: &ruleUpdateRequest{},
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

				if h, ok := l.EventHandlers[common.EventTypes.Subscribe]; ok {
					if resp := h(ctx, params); resp.Error != "" {
						return resp
					}
				}

				// The initial update will be done asynchronously.
				return common.Response{Continuations: []common.ContinuationRequest{{
					InDelaySeconds: 1,
					Action:         "update_rules",
					State:          limacharlie.Dict{},
				}}}
			},
			// An Org unsubscribed.
			common.EventTypes.Unsubscribe: func(ctx context.Context, params core.EventCallbackParams) common.Response {
				org := params.Org
				l.Logger.Info(fmt.Sprintf("unsubscribe from %s", org.GetOID()))

				var lastErr error
				mError := sync.Mutex{}

				// Remove the D&R rule we set up.
				h := limacharlie.NewHiveClient(org)
				if _, err := h.Remove(limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
				}); err != nil && !strings.Contains(err.Error(), "RECORD_NOT_FOUND") {
					l.Logger.Error(fmt.Sprintf("failed to remove scheduling D&R rule: %s", err.Error()))
					mError.Lock()
					lastErr = err
					mError.Unlock()
				}

				// For every namespace, remove rules with matching tags.
				sm := semaphore.NewWeighted(100)
				wg := sync.WaitGroup{}
				remCtx := context.Background()
				for namespace := range simplifiedRuleNamespaces {
					namespace = fmt.Sprintf("dr-%s", namespace)
					rules, err := h.ListMtd(limacharlie.HiveArgs{
						HiveName:     namespace,
						PartitionKey: org.GetOID(),
					})
					if err != nil {
						if !strings.Contains(err.Error(), "UNAUTHORIZED") {
							l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
							mError.Lock()
							lastErr = err
							mError.Unlock()
						}
						continue
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
						if err := sm.Acquire(remCtx, 1); err != nil {
							mError.Lock()
							lastErr = err
							mError.Unlock()
							continue
						}
						wg.Add(1)
						ruleName := ruleName
						go func() {
							defer wg.Done()
							defer sm.Release(1)
							if _, err := h.Remove(limacharlie.HiveArgs{
								HiveName:     namespace,
								PartitionKey: org.GetOID(),
								Key:          ruleName,
							}); err != nil && !strings.Contains(err.Error(), "RECORD_NOT_FOUND") {
								l.Logger.Error(fmt.Sprintf("failed to remove rule %s: %s", ruleName, err.Error()))
								mError.Lock()
								lastErr = err
								mError.Unlock()
							}
						}()
					}
				}

				wg.Wait()

				if h, ok := l.EventHandlers[common.EventTypes.Unsubscribe]; ok {
					if resp := h(ctx, params); resp.Error != "" {
						return resp
					}
				}

				if lastErr != nil {
					return common.Response{Error: lastErr.Error()}
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

func (l *RuleExtension) onUpdate(ctx context.Context, params core.RequestCallbackParams) common.Response {
	h := limacharlie.NewHiveClient(params.Org)

	config := ruleConfig{}
	if err := params.Config.UnMarshalToStruct(&config); err != nil {
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

				// If suppression is set, modify a copy of the rule data.
				ruleToSet := ruleData.Data
				if config.GlobalSuppressionTime != "" {
					ruleToSet = limacharlie.Dict{}
					if _, err := ruleToSet.ImportFromStruct(ruleData.Data); err != nil {
						l.Logger.Error(fmt.Sprintf("failed to duplicate data: %s", err.Error()))
						ruleToSet = ruleData.Data
					} else {
						if ruleToSet = addSuppression(ruleToSet, config.GlobalSuppressionTime); ruleToSet == nil {
							ruleToSet = ruleData.Data
						}
					}
				}
				if isDebugLogRules {
					l.Logger.Info(fmt.Sprintf("rule %s: %s", ruleName, ruleToSet))
				}

				// We need to do a transactional update to check if the
				// rule exists before we set it.
				if _, err := h.UpdateTx(limacharlie.HiveArgs{
					HiveName:     namespace,
					PartitionKey: params.Org.GetOID(),
					Key:          ruleName,
				}, func(record *limacharlie.HiveData) (*limacharlie.HiveData, error) {
					// If the rule does not exist (null), just add
					// it with the enable by default flag.
					if record == nil {
						return &limacharlie.HiveData{
							Data: ruleToSet,
							UsrMtd: limacharlie.UsrMtd{
								Enabled: !config.DisableByDefault,
								Tags:    l.mergeTags(ruleData.Tags, []string{}),
							},
						}, nil
					}
					// If the rule exists, only update its data and upsert
					// its tags. That way if the user disabled it or tagged
					// it, we leave it that way.
					record.Data = ruleToSet
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
				PartitionKey: params.Org.GetOID(),
			})
			if err != nil {
				l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
				return
			}
			for ruleName := range existingRules {
				if _, ok := rules[ruleName]; ok {
					continue
				}
				// Only delete rules with our tag, this avoids
				// mistakes where the extension is not Segmented.
				isRemove := false
				for _, t := range existingRules[ruleName].UsrMtd.Tags {
					if t == l.tag {
						isRemove = true
						break
					}
				}
				if !isRemove {
					continue
				}

				ruleName := ruleName
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := h.Remove(limacharlie.HiveArgs{
						HiveName:     namespace,
						PartitionKey: params.Org.GetOID(),
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

	wg.Wait()

	l.Logger.Info("done installing rules")

	return common.Response{}
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

func addSuppression(rule limacharlie.Dict, suppressionTime string) limacharlie.Dict {
	rrs := rule["respond"]
	if rrs == nil {
		return nil
	}
	rs := rrs.([]interface{})
	if len(rs) == 0 {
		return nil
	}
	// To avoid errors in prod from before the day where
	// we validated the suppression time, we'll just
	// just assume hours if the unit is not set.
	if i, err := strconv.Atoi(suppressionTime); err == nil {
		suppressionTime = fmt.Sprintf("%dh", i)
	}
	for _, r := range rs {
		r, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if r["action"] != "report" {
			continue
		}
		r["suppression"] = limacharlie.Dict{
			"max_count": 1,
			"period":    suppressionTime,
			"is_global": false,
			"keys":      []string{r["name"].(string)},
		}
	}
	return rule
}
