package simplified

import (
	"fmt"
	"strings"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
)

// Resource ACLs and the rules an extension installs.
//
// When a sensor is restricted by a Resource ACL (it carries an acl:<scope>
// tag), the platform refuses the "extension request", "service request" and
// "start ai agent" response actions of a D&R rule for that sensor's events
// unless the rule's acl_scopes lists the scope. An extension cannot name an
// organization's scopes, so it lists the reserved "*" entry instead: the scopes
// the API key that wrote the rule - the extension's own - is a member of,
// looked up when the rule fires. An administrator lets the extension reach a
// restricted sensor by adding the extension's API key to the scope, and
// removing it revokes.
const (
	// aclScopesKey is the field of a rule's content listing the scopes its
	// gated actions may act on.
	aclScopesKey = "acl_scopes"
	// aclScopesAuthorKey is a field the platform adds to a stored rule that
	// lists "*": who wrote it. It is never part of what an extension wants a
	// rule to be.
	aclScopesAuthorKey = "acl_scopes_author"
	// aclScopeRuleAuthor is the reserved acl_scopes entry.
	aclScopeRuleAuthor = "*"
	// aclScopesUnavailable is the error code of a write that can be retried.
	aclScopesUnavailable = "ACL_SCOPES_UNAVAILABLE"
)

// ruleAdder is the part of the HiveClient used to install the recurring update
// rule.
type ruleAdder interface {
	Add(args limacharlie.HiveArgs) (*limacharlie.HiveResp, error)
}

// ruleGetAdder is the part of the HiveClient used to bring an installed
// recurring update rule up to date.
type ruleGetAdder interface {
	ruleAdder
	Get(args limacharlie.HiveArgs) (*limacharlie.HiveData, error)
}

// updateRuleData is the rule asking the platform to call the extension's update
// action on a schedule. It is the extension's own rule, so it lists "*".
func updateRuleData(extensionName string, action string) limacharlie.Dict {
	return limacharlie.Dict{
		"detect": limacharlie.Dict{
			"target": "schedule",
			"event":  "12h_per_org",
			"op":     "exists",
			"path":   "event",
		},
		"respond": []limacharlie.Dict{{
			"action":            "extension request",
			"extension name":    extensionName,
			"extension action":  action,
			"extension request": limacharlie.Dict{},
		}},
		aclScopesKey: []string{aclScopeRuleAuthor},
	}
}

// isACLScopesRefusal reports whether a write was refused because of the rule's
// acl_scopes, as opposed to failing for any other reason. Such a refusal names
// the field, and nothing else about a rule does. ACL_SCOPES_UNAVAILABLE names
// it too but is not one: the platform could not look up scope membership just
// then, and the same write succeeds later.
func isACLScopesRefusal(errMsg string) bool {
	return strings.Contains(errMsg, aclScopesKey) && !strings.Contains(errMsg, aclScopesUnavailable)
}

// withoutACLScopes returns a copy of the rule with its acl_scopes removed, and
// whether it had any.
func withoutACLScopes(rule limacharlie.Dict) (limacharlie.Dict, bool) {
	if _, ok := rule[aclScopesKey]; !ok {
		return rule, false
	}
	stripped := make(limacharlie.Dict, len(rule))
	for k, v := range rule {
		if k != aclScopesKey {
			stripped[k] = v
		}
	}
	return stripped, true
}

// withoutACLScopesAuthor returns the rule without the acl_scopes_author the
// platform added to it, if any.
func withoutACLScopesAuthor(rule limacharlie.Dict) limacharlie.Dict {
	if _, ok := rule[aclScopesAuthorKey]; !ok {
		return rule
	}
	stripped := make(limacharlie.Dict, len(rule))
	for k, v := range rule {
		if k != aclScopesAuthorKey {
			stripped[k] = v
		}
	}
	return stripped
}

// addUpdateRule installs the recurring update rule. A platform that does not
// know the "*" entry refuses the rule, naming the field; the rule without it is
// what was installed before the entry existed, so install that instead. Any
// other failure is returned as is: retrying it without acl_scopes would
// downgrade a rule that already has them.
func addUpdateRule(h ruleAdder, logger limacharlie.LCLogger, args limacharlie.HiveArgs) error {
	_, err := h.Add(args)
	if err == nil || !isACLScopesRefusal(err.Error()) {
		return err
	}
	fallback, hadScopes := withoutACLScopes(args.Data)
	if !hadScopes {
		return err
	}
	logger.Warn(fmt.Sprintf("rule %s was refused with %s (%v); installing it without, so it will not reach sensors restricted by a resource ACL", args.Key, aclScopesKey, err))
	args.Data = fallback
	if _, fallbackErr := h.Add(args); fallbackErr != nil {
		return fmt.Errorf("%w; and without %s: %v", err, aclScopesKey, fallbackErr)
	}
	return nil
}

// upgradeUpdateRule gives a recurring update rule installed before acl_scopes
// existed its acl_scopes. It is called on every update, so a rule that already
// has them costs one read and no write. A rule that is not there is left
// alone: an extension may schedule its updates some other way. Everything else
// about the rule is kept as found, including whether it is enabled.
func upgradeUpdateRule(h ruleGetAdder, logger limacharlie.LCLogger, oid string, ruleName string) {
	args := limacharlie.HiveArgs{
		HiveName:     updateRuleHive,
		PartitionKey: oid,
		Key:          ruleName,
	}
	rec, err := h.Get(args)
	if err != nil {
		if !strings.Contains(err.Error(), "RECORD_NOT_FOUND") && !strings.Contains(err.Error(), "UNAUTHORIZED") {
			logger.Error(fmt.Sprintf("failed to get rule %s: %s", ruleName, err.Error()))
		}
		return
	}
	if rec.Data[aclScopesKey] != nil {
		return
	}
	args.Data = limacharlie.Dict{aclScopesKey: []string{aclScopeRuleAuthor}}
	for k, v := range rec.Data {
		args.Data[k] = v
	}
	args.Enabled = &rec.UsrMtd.Enabled
	args.Tags = rec.UsrMtd.Tags
	args.Expiry = &rec.UsrMtd.Expiry
	args.Comment = &rec.UsrMtd.Comment
	if _, err := h.Add(args); err != nil {
		if isACLScopesRefusal(err.Error()) {
			// The platform does not know the entry yet, and the rule without
			// it is already installed.
			logger.Warn(fmt.Sprintf("rule %s was refused with %s (%v); leaving the installed rule as it is", ruleName, aclScopesKey, err))
			return
		}
		logger.Error(fmt.Sprintf("failed to update rule %s: %s", ruleName, err.Error()))
	}
}
