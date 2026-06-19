package core

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
)

// An InstallationKey matches an Adapter iff:
//   - The descriptions match
//   - The InstallationKey's list of Tags contains the Adapter's PrivateTag
func keyMatchesAdapter(k limacharlie.InstallationKey, desc, privateTag string) bool {
	return k.Description == desc && slices.Contains(k.Tags, privateTag)
}

func (e *Extension) currentAdapterKey(hc *limacharlie.HiveClient, oid string) (string, error) {
	rec, err := hc.Get(limacharlie.HiveArgs{
		HiveName:     "cloud_sensor",
		PartitionKey: oid,
		Key:          e.ExtensionName,
	})
	if err != nil {
		if strings.Contains(err.Error(), "RECORD_NOT_FOUND") {
			// If there is no adapter record for this extension, just return ""
			return "", nil
		}
		return "", fmt.Errorf("reading cloud_sensor record: %w", err)
	}

	// Extract installation key from Data.webhook.client_options.identity.installation_key
	webhook, ok := rec.Data["webhook"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	co, ok := webhook["client_options"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	identity, ok := co["identity"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	installationKey, _ := identity["installation_key"].(string)
	return installationKey, nil
}

func (e *Extension) CreateExtensionAdapter(o *limacharlie.Organization, optMapping limacharlie.Dict) error {
	privateTag := e.GetExtensionPrivateTag()
	desc := e.getExtensionAdapterInstallationKeyDesc()
	oid := o.GetOID()
	hc := limacharlie.NewHiveClient(o)
	isTrue := true
	if optMapping == nil {
		optMapping = limacharlie.Dict{}
	}

	keys, err := o.InstallationKeys()
	if err != nil {
		return fmt.Errorf("failed to list installation keys: %v", err)
	}

	// Get all installation keys that match this extension.
	var matching []limacharlie.InstallationKey
	for _, k := range keys {
		if keyMatchesAdapter(k, desc, privateTag) {
			matching = append(matching, k)
		}
	}

	// Resolve installationKey to either an existing key, or a new one.
	var installationKey string
	if len(matching) > 0 {
		current, err := e.currentAdapterKey(hc, oid)
		if err != nil {
			return err
		}
		// If any of the keys for this extension match the one currently
		// in Hive, use that one.
		for _, k := range matching {
			if k.ID == current {
				installationKey = k.ID
				break
			}
		}

		// Otherwise, just pick one.
		if installationKey == "" {
			installationKey = matching[0].ID
		}

		// Clean up any duplicative keys
		for _, k := range matching {
			if k.ID != installationKey {
				err := o.DelInstallationKey(k.ID)
				if err != nil {
					return fmt.Errorf("failed to delete duplicate installation key: %v", err)
				}
			}
		}
	} else {
		installationKey, err = o.AddInstallationKey(limacharlie.InstallationKey{
			Description: desc,
			Tags:        []string{"lc:system", privateTag},
		})
		if err != nil {
			return fmt.Errorf("failed to create installation key for webhook adapter: %v", err)
		}
	}

	if _, err = hc.Add(limacharlie.HiveArgs{
		HiveName:     "cloud_sensor",
		PartitionKey: oid,
		Key:          e.ExtensionName,
		Enabled:      &isTrue,
		Tags:         []string{"lc:system", privateTag},
		Data: limacharlie.Dict{
			"sensor_type": "webhook",
			"webhook": limacharlie.Dict{
				"secret": e.generateWebhookSecretForOrg(oid),
				"client_options": limacharlie.Dict{
					"hostname": e.ExtensionName,
					"identity": limacharlie.Dict{
						"oid":              oid,
						"installation_key": installationKey,
					},
					"platform":        "json",
					"sensor_seed_key": e.ExtensionName,
					"mapping":         optMapping,
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to create webhook adapter: %v", err)
	}
	return nil
}

func (e *Extension) DeleteExtensionAdapter(o *limacharlie.Organization) error {
	privateTag := e.GetExtensionPrivateTag()

	hc := limacharlie.NewHiveClient(o)
	if _, err := hc.Remove(limacharlie.HiveArgs{
		HiveName:     "cloud_sensor",
		PartitionKey: o.GetOID(),
		Key:          e.ExtensionName,
	}); err != nil && !strings.Contains(err.Error(), "RECORD_NOT_FOUND") {
		return fmt.Errorf("failed to del webhook: %v", err)
	}

	keys, err := o.InstallationKeys()
	if err != nil {
		return fmt.Errorf("failed to list installation keys: %v", err)
	}

	instKeyDesc := e.getExtensionAdapterInstallationKeyDesc()

	for _, key := range keys {
		if key.Description != instKeyDesc {
			continue
		}
		isTagFound := false
		for _, t := range key.Tags {
			if t == privateTag {
				isTagFound = true
				break
			}
		}
		if !isTagFound {
			continue
		}
		if err := o.DelInstallationKey(key.ID); err != nil {
			return fmt.Errorf("failed to delete installation key: %v", err)
		}
	}
	return nil
}

func (e *Extension) generateWebhookSecretForOrg(oid string) string {
	// This generates a secret value deterministically from
	// the OID so that we can easily knnow the webhook to
	// hit without having to query LC. The WEBHOOK_SECRET
	// needs to remain secret to avoid someone possibly
	// sending their own data to users.
	h := sha256.New()
	h.Write([]byte(e.SecretKey))
	h.Write([]byte(oid))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (e *Extension) getExtensionAdapterInstallationKeyDesc() string {
	return fmt.Sprintf("ext %s webhook adapter", e.ExtensionName)
}

func (e *Extension) getAdapterClient(o *limacharlie.Organization) (*limacharlie.WebhookSender, error) {
	oid := o.GetOID()

	e.mWebhooks.RLock()
	c, ok := e.whClients[oid]
	e.mWebhooks.RUnlock()

	if ok {
		return c, nil
	}

	newClient, err := o.NewWebhookSender(e.ExtensionName, e.generateWebhookSecretForOrg(oid))
	if err != nil {
		return nil, err
	}

	e.mWebhooks.Lock()
	defer e.mWebhooks.Unlock()
	c, ok = e.whClients[oid]
	if ok {
		newClient.Close()
		return c, nil
	}
	e.whClients[oid] = newClient
	return newClient, nil
}

func (e *Extension) SendToWebhookAdapter(o *limacharlie.Organization, data interface{}) error {
	whClient, err := e.getAdapterClient(o)
	if err != nil {
		return err
	}
	if err := whClient.Send(data); err != nil {
		return err
	}
	return nil
}
