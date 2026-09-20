package simplified

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
	"github.com/refractionPOINT/lc-extension/core"
)

type (
	GetRulesCallback = func(ctx context.Context) (RuleData, error)
	RuleName         = string
	RuleNamespace    = string
	RuleInfo         struct {
		Tags []string
		Data limacharlie.Dict
	}
)
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
				if d < 1*time.Second {
					return common.Response{Error: "global suppression time cannot be less than 1s"}
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
				if err := addUpdateRule(h, l.Logger, limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
					Data:         updateRuleData(l.Name, "update_rules"),
					Tags:         []string{l.tag},
					Enabled:      &trueValue,
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

				// Remove the D&R rule we set up.
				h := limacharlie.NewHiveClient(org)
				if _, err := h.Remove(limacharlie.HiveArgs{
					HiveName:     updateRuleHive,
					PartitionKey: org.GetOID(),
					Key:          l.ruleName,
				}); err != nil && !strings.Contains(err.Error(), "RECORD_NOT_FOUND") {
					l.Logger.Error(fmt.Sprintf("failed to remove scheduling D&R rule: %s", err.Error()))
				}

				// For every namespace, remove rules with matching tags.
				batchUpdate := h.NewBatchOperations()
				for namespace := range simplifiedRuleNamespaces {
					namespace = fmt.Sprintf("dr-%s", namespace)
					rules, err := h.ListMtd(limacharlie.HiveArgs{
						HiveName:     namespace,
						PartitionKey: org.GetOID(),
					})
					if err != nil {
						if !strings.Contains(err.Error(), "UNAUTHORIZED") {
							l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
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
						batchUpdate.DelRecord(limacharlie.RecordID{
							Hive: limacharlie.HiveID{
								Name:      limacharlie.HiveName(namespace),
								Partition: limacharlie.PartitionID(org.GetOID()),
							},
							Name: limacharlie.RecordName(ruleName),
						})
					}
				}

				ops, err := batchUpdate.Execute()
				if err != nil {
					l.Logger.Error(fmt.Sprintf("failed to remove rules: %s", err.Error()))
				}
				for _, op := range ops {
					if op.Error != "" && !strings.Contains(op.Error, "RECORD_NOT_FOUND") {
						l.Logger.Error(fmt.Sprintf("failed to remove rule: %s", err.Error()))
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

func (l *RuleExtension) onUpdate(ctx context.Context, params core.RequestCallbackParams) common.Response {
	h := limacharlie.NewHiveClient(params.Org)

	config := ruleConfig{}
	if err := params.Config.UnMarshalToStruct(&config); err != nil {
		return common.Response{Error: err.Error()}
	}

	// A recurring update rule installed before acl_scopes existed gets them.
	upgradeUpdateRule(h, l.Logger, params.Org.GetOID(), l.ruleName)

	return l.updateRules(ctx, hiveRuleStore{h}, params.Org.GetOID(), config)
}

// ruleStore is the part of the HiveClient used to reconcile the rules.
type ruleStore interface {
	List(args limacharlie.HiveArgs) (limacharlie.HiveConfigData, error)
	NewBatch() ruleBatch
}

// ruleBatch is the part of a HiveBatch used to reconcile the rules. Execute
// answers every operation, in the order they were added.
type ruleBatch interface {
	SetRecord(record limacharlie.RecordID, config limacharlie.ConfigRecordMutation)
	DelRecord(record limacharlie.RecordID)
	Execute() ([]limacharlie.BatchResponse, error)
}

type hiveRuleStore struct {
	*limacharlie.HiveClient
}

func (h hiveRuleStore) NewBatch() ruleBatch {
	return h.NewBatchOperations()
}

// setRuleOp is a rule write added to a batch, remembered so that the answer to
// it can be told apart from the others.
type setRuleOp struct {
	record   limacharlie.RecordID
	mutation limacharlie.ConfigRecordMutation
	// isInstalledWithoutScopes is set when the rule is already in Hive as
	// wanted except for its acl_scopes.
	isInstalledWithoutScopes bool
}

func (l *RuleExtension) updateRules(ctx context.Context, h ruleStore, oid string, config ruleConfig) common.Response {

	rulesData, err := l.GetRules(ctx)
	if err != nil {
		return common.Response{Error: err.Error()}
	}

	suppTime := l.shimSuppressionTime(config.GlobalSuppressionTime)

	// Apply the suppression time to all rules.
	batchUpdate := h.NewBatch()
	// The operations added to the batch, in order: nil for a removal.
	batchOps := []*setRuleOp{}
	for namespace, rules := range rulesData {
		// Fetch all the rules in Hive for the given namespace.
		hiveName := fmt.Sprintf("dr-%s", namespace)
		existing, err := h.List(limacharlie.HiveArgs{
			HiveName:     hiveName,
			PartitionKey: oid,
		})
		if err != nil {
			l.Logger.Error(fmt.Sprintf("failed to list rules: %s", err.Error()))
			continue
		}

		// Diff the rule contents with the rules in Hive.
		for ruleName, ruleData := range rules {
			// Add in our suppression.
			ruleToSet := ruleData.Data
			if suppTime != "" {
				ruleToSet = limacharlie.Dict{}
				if _, err := ruleToSet.ImportFromStruct(ruleData.Data); err != nil {
					l.Logger.Error(fmt.Sprintf("failed to duplicate data: %s", err.Error()))
					ruleToSet = ruleData.Data
				} else {
					if ruleToSet = addSuppression(ruleToSet, suppTime); ruleToSet == nil {
						ruleToSet = ruleData.Data
					}
				}
			}

			// Do we have that rule name in hive already?
			// If not, we'll add it.
			// If we do, diff it and update it if needed.
			if existingRule, ok := existing[ruleName]; !ok {
				op := &setRuleOp{
					record: limacharlie.RecordID{
						Hive: limacharlie.HiveID{
							Name:      limacharlie.HiveName(hiveName),
							Partition: limacharlie.PartitionID(oid),
						},
						Name: limacharlie.RecordName(ruleName),
					},
					mutation: limacharlie.ConfigRecordMutation{
						Data: ruleToSet,
						UsrMtd: &limacharlie.UsrMtd{
							Enabled: !config.DisableByDefault,
							Tags:    l.mergeTags(ruleData.Tags, []string{}),
						},
					},
				}
				batchUpdate.SetRecord(op.record, op.mutation)
				batchOps = append(batchOps, op)
				if isDebugLogRules {
					l.Logger.Info(fmt.Sprintf("adding rule %s: %s", ruleName, ruleToSet))
				}
			} else if !areEqual(ruleToSet, existingRule.Data) {
				// The rule is there but has changed.
				if isDebugLogRules {
					l.Logger.Info(fmt.Sprintf("updating rule %s: %s", ruleName, ruleToSet))
				}
				op := &setRuleOp{
					record: limacharlie.RecordID{
						Hive: limacharlie.HiveID{
							Name:      limacharlie.HiveName(hiveName),
							Partition: limacharlie.PartitionID(oid),
						},
						Name: limacharlie.RecordName(ruleName),
					},
					mutation: limacharlie.ConfigRecordMutation{
						Data: ruleToSet,
						UsrMtd: &limacharlie.UsrMtd{
							Enabled: !config.DisableByDefault,
							Tags:    l.mergeTags(ruleData.Tags, []string{}),
						},
					},
				}
				if withoutScopes, hasScopes := withoutACLScopes(ruleToSet); hasScopes {
					op.isInstalledWithoutScopes = areEqual(withoutScopes, existingRule.Data)
				}
				batchUpdate.SetRecord(op.record, op.mutation)
				batchOps = append(batchOps, op)
			}
		}

		// Now check for rules that exist in Hive but not in our list.
		for ruleName := range existing {
			if _, ok := rules[ruleName]; ok {
				continue
			}
			// Only delete rules with our tag, this avoids
			// mistakes where the extension is not Segmented.
			isRemove := false
			for _, t := range existing[ruleName].UsrMtd.Tags {
				if t == l.tag {
					isRemove = true
					break
				}
			}
			if !isRemove {
				continue
			}
			batchUpdate.DelRecord(limacharlie.RecordID{
				Hive: limacharlie.HiveID{
					Name:      limacharlie.HiveName(hiveName),
					Partition: limacharlie.PartitionID(oid),
				},
				Name: limacharlie.RecordName(ruleName),
			})
			batchOps = append(batchOps, nil)
		}
	}

	// Apply the changes.
	ops, err := batchUpdate.Execute()
	if err != nil {
		l.Logger.Error(fmt.Sprintf("failed to update rules: %s", err.Error()))
		return common.Response{Error: err.Error()}
	}
	// A platform that does not know the "*" acl_scopes entry refuses a rule
	// listing it, naming the field. The rule without acl_scopes is what an
	// extension installed before the entry existed, so install that instead
	// of leaving the rule out. Any other failure is only reported: retrying it
	// without acl_scopes would downgrade a rule that already has them.
	batchRetry := h.NewBatch()
	retried := []*limacharlie.BatchResponse{}
	for i := range ops {
		op := &ops[i]
		if op.Error == "" {
			continue
		}
		var set *setRuleOp
		if len(ops) == len(batchOps) {
			set = batchOps[i]
		}
		if set == nil || !isACLScopesRefusal(op.Error) {
			l.Logger.Error(fmt.Sprintf("failed to update rule: %s", op.Error))
			continue
		}
		withoutScopes, hasScopes := withoutACLScopes(set.mutation.Data)
		if !hasScopes {
			l.Logger.Error(fmt.Sprintf("failed to update rule: %s", op.Error))
			continue
		}
		if set.isInstalledWithoutScopes {
			l.Logger.Warn(fmt.Sprintf("rule %s was refused with %s (%s); leaving the installed rule as it is", set.record.Name, aclScopesKey, op.Error))
			continue
		}
		l.Logger.Warn(fmt.Sprintf("rule %s was refused with %s (%s); installing it without, so it will not reach sensors restricted by a resource ACL", set.record.Name, aclScopesKey, op.Error))
		set.mutation.Data = withoutScopes
		batchRetry.SetRecord(set.record, set.mutation)
		retried = append(retried, op)
	}
	retryOps, err := batchRetry.Execute()
	if err != nil {
		l.Logger.Error(fmt.Sprintf("failed to update rules without %s: %s", aclScopesKey, err.Error()))
		return common.Response{Error: err.Error()}
	}
	for i, op := range retryOps {
		if op.Error == "" {
			continue
		}
		if i < len(retried) {
			l.Logger.Error(fmt.Sprintf("failed to update rule: %s; and without %s: %s", retried[i].Error, aclScopesKey, op.Error))
			continue
		}
		l.Logger.Error(fmt.Sprintf("failed to update rule: %s", op.Error))
	}

	l.Logger.Info("done updating rules")

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

func (l *RuleExtension) shimSuppressionTime(st string) string {
	if st == "" {
		return ""
	}
	d, err := time.ParseDuration(st)
	if err != nil {
		i, err := strconv.Atoi(st)
		if err != nil {
			l.Logger.Error(fmt.Sprintf("invalid suppression time: %q", st))
			return ""
		}
		// To avoid errors in prod from before the day where
		// we validated the suppression time, we'll just
		// just assume hours if the unit is not set.
		return fmt.Sprintf("%dh", i)
	}
	if d < 1*time.Second {
		return ""
	}
	return st
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

// Compares two dictionaries and their values for equality accounting for field order.
// acl_scopes_author is set aside: the platform adds it to a stored rule whose
// acl_scopes lists "*", so it is never a difference between the rule an
// extension wants and the one in Hive.
func areEqual(d1 limacharlie.Dict, d2 limacharlie.Dict) bool {
	// The json lib will sort keys of maps when serializing (not structs).
	// So we can just compare the serialized versions.
	s1, err := json.Marshal(withoutACLScopesAuthor(d1))
	if err != nil {
		return false
	}
	s2, err := json.Marshal(withoutACLScopesAuthor(d2))
	if err != nil {
		return false
	}
	return string(s1) == string(s2)
}
