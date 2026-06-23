package core

import (
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
)

func TestIdempotentCreateExtensionAdapter(t *testing.T) {
	ms := limacharlie.NewMockServer("oid-test")
	defer ms.Close()
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatalf("TestIdempotentCreateExtensionAdapter error: %s", err)
	}
	ext := &Extension{ExtensionName: "my-ext", SecretKey: "secret"}

	// Call CreateExtensionAdapter() multiple times to exercise idempotency
	for range 3 {
		if err := ext.CreateExtensionAdapter(org, limacharlie.Dict{}); err != nil {
			t.Fatalf("CreateExtensionAdapter failed: %s", err)
		}
	}

	// Verify that only one key exists in InstallationKeyStore
	numKeys := len(ms.InstallationKeyStore)
	if numKeys != 1 {
		t.Errorf("TestIdempotentCreateExtensionAdapter error: InstallationKeyStore has %d keys, wanted 1", numKeys)
	}
}

func TestCreateExtensionAdapterDuplicateKeyCleanup(t *testing.T) {
	ms := limacharlie.NewMockServer("oid-test")
	defer ms.Close()
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatalf("TestCreateExtensionAdapterDuplicateKeyCleanup error: %s", err)
	}
	ext := &Extension{ExtensionName: "my-ext", SecretKey: "secret"}
	desc := ext.getExtensionAdapterInstallationKeyDesc()
	tag := ext.GetExtensionPrivateTag()

	// Pre-seed some duplicative installation keys
	for _, iid := range []string{"iid-1", "iid-2", "iid-3"} {
		ms.InstallationKeyStore[iid] = limacharlie.InstallationKey{
			ID:          iid,
			Description: desc,
			Tags:        []string{"lc:system", tag},
		}
	}

	if err := ext.CreateExtensionAdapter(org, limacharlie.Dict{}); err != nil {
		t.Fatalf("CreateExtensionAdapter failed: %s", err)
	}

	// Verify that only one key remains in InstallationKeyStore
	numKeys := len(ms.InstallationKeyStore)
	if numKeys != 1 {
		t.Errorf("TestCreateExtensionAdapterDuplicateKeyCleanup error: InstallationKeyStore has %d keys, wanted 1", numKeys)
	}
}

func TestCreateExtensionAdapterActiveKeyRemains(t *testing.T) {
	const oid = "oid-test"
	ms := limacharlie.NewMockServer(oid)
	defer ms.Close()
	org, err := ms.NewOrganization()
	if err != nil {
		t.Fatalf("TestCreateExtensionAdapterActiveKeyRemains error: %s", err)
	}
	ext := &Extension{ExtensionName: "my-ext", SecretKey: "secret"}
	desc := ext.getExtensionAdapterInstallationKeyDesc()
	tag := ext.GetExtensionPrivateTag()

	// Pre-seed some duplicative installation keys
	for _, iid := range []string{"iid-1", "iid-2", "iid-3"} {
		ms.InstallationKeyStore[iid] = limacharlie.InstallationKey{
			ID:          iid,
			Description: desc,
			Tags:        []string{"lc:system", tag},
		}
	}

	// Add a Hive record pointing to iid-2
	hiveRecord := map[string]limacharlie.HiveData{
		ext.ExtensionName: {
			Data: map[string]interface{}{
				"webhook": map[string]interface{}{
					"client_options": map[string]interface{}{
						"identity": map[string]interface{}{
							"installation_key": "iid-2",
						},
					},
				},
			},
		},
	}
	ms.HiveStore["cloud_sensor/"+oid] = hiveRecord

	if err := ext.CreateExtensionAdapter(org, limacharlie.Dict{}); err != nil {
		t.Fatalf("CreateExtensionAdapter failed: %s", err)
	}

	// Verify that only one key remains in InstallationKeyStore...
	numKeys := len(ms.InstallationKeyStore)
	if numKeys != 1 {
		t.Errorf("TestCreateExtensionAdapterActiveKeyRemains error: InstallationKeyStore has %d keys, wanted 1", numKeys)
	}

	// ...and that it's iid-2
	_, ok := ms.InstallationKeyStore["iid-2"]
	if !ok {
		t.Errorf("TestCreateExtensionAdapterActiveKeyRemains error: active key iid-2 was deleted")
	}
}
