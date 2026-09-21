package simplified

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
)

// recordingLogger keeps what was logged at the levels the tests look at.
type recordingLogger struct {
	limacharlie.LCLoggerEmpty
	warnings []string
	errors   []string
}

func (l *recordingLogger) Warn(msg string)  { l.warnings = append(l.warnings, msg) }
func (l *recordingLogger) Error(msg string) { l.errors = append(l.errors, msg) }

// fakeHive stands in for the platform: it serves stored records, records every
// write, and refuses the ones refuse says to.
type fakeHive struct {
	stored map[string]limacharlie.HiveData
	// refuse returns the error for a write of this rule content, or "".
	refuse func(name string, data limacharlie.Dict) string

	adds    []limacharlie.HiveArgs
	batches [][]fakeOp
}

type fakeOp struct {
	name     string
	isDelete bool
	data     limacharlie.Dict
}

func (h *fakeHive) refusal(name string, data limacharlie.Dict) string {
	if h.refuse == nil {
		return ""
	}
	return h.refuse(name, data)
}

func (h *fakeHive) Get(args limacharlie.HiveArgs) (*limacharlie.HiveData, error) {
	rec, ok := h.stored[args.Key]
	if !ok {
		return nil, errors.New("RECORD_NOT_FOUND")
	}
	return &rec, nil
}

func (h *fakeHive) Add(args limacharlie.HiveArgs) (*limacharlie.HiveResp, error) {
	h.adds = append(h.adds, args)
	if msg := h.refusal(args.Key, args.Data); msg != "" {
		return nil, errors.New(msg)
	}
	return &limacharlie.HiveResp{}, nil
}

func (h *fakeHive) List(args limacharlie.HiveArgs) (limacharlie.HiveConfigData, error) {
	return h.stored, nil
}

func (h *fakeHive) NewBatch() ruleBatch {
	return &fakeBatch{h: h}
}

type fakeBatch struct {
	h   *fakeHive
	ops []fakeOp
}

func (b *fakeBatch) SetRecord(record limacharlie.RecordID, config limacharlie.ConfigRecordMutation) {
	b.ops = append(b.ops, fakeOp{name: string(record.Name), data: config.Data})
}

func (b *fakeBatch) DelRecord(record limacharlie.RecordID) {
	b.ops = append(b.ops, fakeOp{name: string(record.Name), isDelete: true})
}

func (b *fakeBatch) Execute() ([]limacharlie.BatchResponse, error) {
	b.h.batches = append(b.h.batches, b.ops)
	responses := []limacharlie.BatchResponse{}
	for _, op := range b.ops {
		resp := limacharlie.BatchResponse{}
		if !op.isDelete {
			resp.Error = b.h.refusal(op.name, op.data)
		}
		responses = append(responses, resp)
	}
	return responses, nil
}

// refuseACLScopes is a platform that does not know the "*" entry.
func refuseACLScopes(name string, data limacharlie.Dict) string {
	if _, ok := data[aclScopesKey]; ok {
		return "invalid acl_scopes: unknown scope *"
	}
	return ""
}

func gatedRule(withScopes bool) limacharlie.Dict {
	rule := limacharlie.Dict{
		"detect": limacharlie.Dict{"event": "NEW_PROCESS", "op": "exists", "path": "event"},
		"respond": []limacharlie.Dict{{
			"action":            "extension request",
			"extension name":    "ext-test",
			"extension action":  "scan",
			"extension request": limacharlie.Dict{},
		}},
	}
	if withScopes {
		rule[aclScopesKey] = []string{aclScopeRuleAuthor}
	}
	return rule
}

// asStored is the rule as the platform hands it back: through JSON, and with
// the author it recorded for a rule listing "*".
func asStored(t *testing.T, rule limacharlie.Dict) limacharlie.HiveData {
	stored := limacharlie.Dict{}
	if _, err := stored.ImportFromStruct(rule); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored[aclScopesKey]; ok {
		stored[aclScopesAuthorKey] = "ext-test-key"
	}
	return limacharlie.HiveData{Data: stored, UsrMtd: limacharlie.UsrMtd{Enabled: true, Tags: []string{"ext:ext-test"}}}
}

func newTestRuleExtension(logger limacharlie.LCLogger, rules map[RuleName]RuleInfo) *RuleExtension {
	return &RuleExtension{
		Name:   "ext-test",
		Logger: logger,
		tag:    "ext:ext-test",
		GetRules: func(ctx context.Context) (RuleData, error) {
			return RuleData{"general": rules}, nil
		},
	}
}

func hasRuleAuthorScope(data limacharlie.Dict) bool {
	scopes, ok := data[aclScopesKey].([]string)
	return ok && len(scopes) == 1 && scopes[0] == aclScopeRuleAuthor
}

func TestUpdateRuleListsRuleAuthorScope(t *testing.T) {
	for _, action := range []string{"update_rules", "update_lookup"} {
		if !hasRuleAuthorScope(updateRuleData("ext-test", action)) {
			t.Errorf("the %s rule does not list %q in %s", action, aclScopeRuleAuthor, aclScopesKey)
		}
	}
}

func TestAddUpdateRuleFallsBackOnlyWhenACLScopesAreRefused(t *testing.T) {
	args := limacharlie.HiveArgs{HiveName: updateRuleHive, PartitionKey: "oid", Key: "ext-ext-test-update", Data: updateRuleData("ext-test", "update_rules")}

	// Refused for its acl_scopes: installed without them.
	logger := &recordingLogger{}
	h := &fakeHive{refuse: refuseACLScopes}
	if err := addUpdateRule(h, logger, args); err != nil {
		t.Fatalf("addUpdateRule: %v", err)
	}
	if len(h.adds) != 2 {
		t.Fatalf("expected the rule to be written again, got %d writes", len(h.adds))
	}
	retry := h.adds[1]
	if _, ok := retry.Data[aclScopesKey]; ok || retry.Data["detect"] == nil || retry.Data["respond"] == nil {
		t.Errorf("expected the same rule without %s, got %v", aclScopesKey, retry.Data)
	}
	if retry.Key != args.Key || retry.HiveName != args.HiveName {
		t.Errorf("retry went to %s/%s", retry.HiveName, retry.Key)
	}
	if len(logger.warnings) != 1 {
		t.Errorf("expected one warning, got %v", logger.warnings)
	}

	// Any other failure: not written again without them.
	h = &fakeHive{refuse: func(string, limacharlie.Dict) string { return "503 service unavailable" }}
	if err := addUpdateRule(h, &recordingLogger{}, args); err == nil {
		t.Error("expected the failure to be returned")
	}
	if len(h.adds) != 1 {
		t.Errorf("a failed write must not be retried without %s, got %d writes", aclScopesKey, len(h.adds))
	}

	// Scope membership could not be looked up just then: the error names the
	// field, but the write is to be retried as it is, not downgraded.
	h = &fakeHive{refuse: func(string, limacharlie.Dict) string {
		return "ACL_SCOPES_UNAVAILABLE - cannot evaluate acl_scopes on dr-managed/ext-ext-test-update; the write was refused and can be retried"
	}}
	if err := addUpdateRule(h, &recordingLogger{}, args); err == nil {
		t.Error("expected the failure to be returned")
	}
	if len(h.adds) != 1 {
		t.Errorf("a write that can be retried must not be downgraded, got %d writes", len(h.adds))
	}

	// The fallback fails too: both errors are reported.
	calls := 0
	h = &fakeHive{refuse: func(string, limacharlie.Dict) string {
		calls++
		if calls == 1 {
			return "invalid acl_scopes"
		}
		return "hive down"
	}}
	err := addUpdateRule(h, &recordingLogger{}, args)
	if err == nil || !strings.Contains(err.Error(), "invalid acl_scopes") || !strings.Contains(err.Error(), "hive down") {
		t.Errorf("expected both errors, got %v", err)
	}
}

func TestUpgradeUpdateRule(t *testing.T) {
	const ruleName = "ext-ext-test-update"
	canonical := updateRuleData("ext-test", "update_rules")
	before, _ := withoutACLScopes(canonical)

	// Installed before acl_scopes existed, and turned off since.
	installed := asStored(t, before)
	installed.UsrMtd.Enabled = false
	h := &fakeHive{stored: map[string]limacharlie.HiveData{ruleName: installed}}
	upgradeUpdateRule(h, &recordingLogger{}, "oid", ruleName, canonical)
	if len(h.adds) != 1 {
		t.Fatalf("expected the rule to be written once, got %d", len(h.adds))
	}
	got := h.adds[0]
	if !hasRuleAuthorScope(got.Data) || got.Data["detect"] == nil || got.Data["respond"] == nil {
		t.Errorf("expected the installed rule plus %s, got %v", aclScopesKey, got.Data)
	}
	if got.HiveName != updateRuleHive || got.Key != ruleName {
		t.Errorf("wrote %s/%s", got.HiveName, got.Key)
	}
	if got.Enabled == nil || *got.Enabled {
		t.Error("a rule that was turned off must stay off")
	}

	// Already has them: nothing to write.
	h = &fakeHive{stored: map[string]limacharlie.HiveData{ruleName: asStored(t, updateRuleData("ext-test", "update_rules"))}}
	upgradeUpdateRule(h, &recordingLogger{}, "oid", ruleName, canonical)
	if len(h.adds) != 0 {
		t.Errorf("expected no write, got %d", len(h.adds))
	}

	// Not installed (the extension schedules its updates some other way): not
	// installed here either.
	h = &fakeHive{}
	upgradeUpdateRule(h, &recordingLogger{}, "oid", ruleName, canonical)
	if len(h.adds) != 0 {
		t.Errorf("expected no write, got %d", len(h.adds))
	}

	// Refused: the installed rule is left alone.
	h = &fakeHive{stored: map[string]limacharlie.HiveData{ruleName: asStored(t, before)}, refuse: refuseACLScopes}
	upgradeUpdateRule(h, &recordingLogger{}, "oid", ruleName, canonical)
	if len(h.adds) != 1 {
		t.Errorf("expected the one refused write, got %d", len(h.adds))
	}

	// Edited by the organization: left alone, scopes and all. The rule lives in
	// a hive the organization can edit and removing acl_scopes is always
	// allowed, so writing back what is stored plus the entry would have this
	// extension's key vouch for whatever was put there.
	edited := limacharlie.Dict{}
	for k, v := range before {
		edited[k] = v
	}
	edited["respond"] = []limacharlie.Dict{{
		"action":            "extension request",
		"extension name":    "somebody-elses-extension",
		"extension action":  "run",
		"extension request": limacharlie.Dict{},
	}}
	logger := &recordingLogger{}
	h = &fakeHive{stored: map[string]limacharlie.HiveData{ruleName: asStored(t, edited)}}
	upgradeUpdateRule(h, logger, "oid", ruleName, canonical)
	if len(h.adds) != 0 {
		t.Fatalf("an edited rule must not be written back with %s: wrote %v", aclScopesKey, h.adds)
	}
	if len(logger.warnings) == 0 {
		t.Error("expected the skipped rule to be reported")
	}

	// What is written is the extension's own rule, not what was stored: a
	// stored rule that only differs in a way areEqual ignores is still
	// replaced by the canonical content.
	h = &fakeHive{stored: map[string]limacharlie.HiveData{ruleName: asStored(t, before)}}
	upgradeUpdateRule(h, &recordingLogger{}, "oid", ruleName, canonical)
	if len(h.adds) != 1 {
		t.Fatalf("expected one write, got %d", len(h.adds))
	}
	if !areEqual(h.adds[0].Data, canonical) {
		t.Errorf("wrote %v, want the extension's own rule %v", h.adds[0].Data, canonical)
	}
}

func TestUpdateRulesDoesNotRewriteRuleOverItsAuthor(t *testing.T) {
	rules := map[RuleName]RuleInfo{"gated": {Data: gatedRule(true)}}
	h := &fakeHive{stored: map[string]limacharlie.HiveData{"gated": asStored(t, gatedRule(true))}}
	if resp := newTestRuleExtension(&recordingLogger{}, rules).updateRules(context.Background(), h, "oid", ruleConfig{}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	for _, batch := range h.batches {
		if len(batch) != 0 {
			t.Errorf("expected nothing to be written, got %v", batch)
		}
	}

	// A real difference is still written.
	changed := gatedRule(true)
	changed["detect"] = limacharlie.Dict{"event": "DNS_REQUEST", "op": "exists", "path": "event"}
	h = &fakeHive{stored: map[string]limacharlie.HiveData{"gated": asStored(t, changed)}}
	newTestRuleExtension(&recordingLogger{}, rules).updateRules(context.Background(), h, "oid", ruleConfig{})
	if len(h.batches) == 0 || len(h.batches[0]) != 1 || h.batches[0][0].name != "gated" {
		t.Errorf("expected the changed rule to be written, got %v", h.batches)
	}
}

func TestUpdateRulesWritesRulesAsSupplied(t *testing.T) {
	rules := map[RuleName]RuleInfo{"gated": {Data: gatedRule(true)}, "plain": {Data: gatedRule(false)}}
	h := &fakeHive{}
	newTestRuleExtension(&recordingLogger{}, rules).updateRules(context.Background(), h, "oid", ruleConfig{})
	if len(h.batches) == 0 || len(h.batches[0]) != 2 {
		t.Fatalf("expected both rules to be written, got %v", h.batches)
	}
	for _, op := range h.batches[0] {
		if _, hasScopes := op.data[aclScopesKey]; hasScopes != (op.name == "gated") {
			t.Errorf("rule %s was not written as supplied: %v", op.name, op.data)
		}
	}
}

func TestUpdateRulesRetriesOnlyRulesRefusedForTheirACLScopes(t *testing.T) {
	rules := map[RuleName]RuleInfo{
		"gated":  {Data: gatedRule(true)},
		"broken": {Data: gatedRule(true)},
		"plain":  {Data: gatedRule(false)},
	}
	logger := &recordingLogger{}
	h := &fakeHive{
		// "gone" is removed by the update, so the batch mixes removals and writes.
		stored: map[string]limacharlie.HiveData{"gone": {UsrMtd: limacharlie.UsrMtd{Tags: []string{"ext:ext-test"}}}},
		refuse: func(name string, data limacharlie.Dict) string {
			if name == "broken" {
				return "503 service unavailable"
			}
			return refuseACLScopes(name, data)
		},
	}
	newTestRuleExtension(logger, rules).updateRules(context.Background(), h, "oid", ruleConfig{})

	if len(h.batches) != 2 {
		t.Fatalf("expected a second batch, got %d", len(h.batches))
	}
	retry := h.batches[1]
	if len(retry) != 1 || retry[0].name != "gated" {
		t.Fatalf("expected only the rule refused for its %s to be written again, got %v", aclScopesKey, retry)
	}
	if _, ok := retry[0].data[aclScopesKey]; ok || retry[0].data["detect"] == nil || retry[0].data["respond"] == nil {
		t.Errorf("expected the same rule without %s, got %v", aclScopesKey, retry[0].data)
	}
	if len(logger.warnings) != 1 || !strings.Contains(logger.warnings[0], "gated") {
		t.Errorf("expected a warning about the rule, got %v", logger.warnings)
	}
	if len(logger.errors) != 1 || !strings.Contains(logger.errors[0], "503") {
		t.Errorf("expected the other failure to be reported as before, got %v", logger.errors)
	}
}

func TestUpdateRulesLeavesRuleAlreadyInstalledWithoutACLScopes(t *testing.T) {
	rules := map[RuleName]RuleInfo{"gated": {Data: gatedRule(true)}}
	h := &fakeHive{
		stored: map[string]limacharlie.HiveData{"gated": asStored(t, gatedRule(false))},
		refuse: refuseACLScopes,
	}
	newTestRuleExtension(&recordingLogger{}, rules).updateRules(context.Background(), h, "oid", ruleConfig{})
	for i, batch := range h.batches {
		if i > 0 && len(batch) != 0 {
			t.Errorf("the rule is already installed without %s, expected no second write, got %v", aclScopesKey, batch)
		}
	}
}

func TestUpdateRulesReportsBothErrorsWhenTheFallbackFails(t *testing.T) {
	rules := map[RuleName]RuleInfo{"gated": {Data: gatedRule(true)}}
	logger := &recordingLogger{}
	h := &fakeHive{refuse: func(name string, data limacharlie.Dict) string {
		if _, ok := data[aclScopesKey]; ok {
			return "invalid acl_scopes"
		}
		return "hive down"
	}}
	newTestRuleExtension(logger, rules).updateRules(context.Background(), h, "oid", ruleConfig{})
	if len(logger.errors) != 1 || !strings.Contains(logger.errors[0], "invalid acl_scopes") || !strings.Contains(logger.errors[0], "hive down") {
		t.Errorf("expected one error carrying both failures, got %v", logger.errors)
	}
}
