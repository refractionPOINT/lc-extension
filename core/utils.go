package core

import (
	"fmt"
	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"path"
	"strings"
	"sync"
)

var (
	SecretCache = make(map[string]string)
	cacheMutex  sync.RWMutex
)

func usingSecretValue(key string, org *limacharlie.Organization, fn func(val string) error) error {
	var err error
	var apiKey string
	var exists bool
	if strings.Contains(key, "hive://") {
		secretName := path.Base(key)
		// Try to get secret from cache
		apiKey, exists = getSecretFromCache(secretName)
		if !exists {
			apiKey, err = getSecretFromHive(secretName, org)
			if err != nil {
				return err
			}

			// ensure to update local cache
			setSecretCache(secretName, apiKey)
		}
	}

	const maxRetries = 2
	var lastError error
	for i := 0; i < maxRetries; i++ {
		err := fn(apiKey)
		if err == nil {
			return nil
		}
		lastError = err
	}

	return fmt.Errorf("secrets function failed after %d attempts for org: %s err: %v", maxRetries, org.GetOID(), lastError)
}

func setSecretCache(secretName, apiKey string) {
	cacheMutex.Lock()
	SecretCache[secretName] = apiKey
	cacheMutex.Unlock()
}

func getSecretFromCache(secretName string) (string, bool) {
	cacheMutex.RLock()
	val, exists := SecretCache[secretName]
	cacheMutex.RUnlock()
	return val, exists
}

func getSecretFromHive(recordName string, org *limacharlie.Organization) (string, error) {
	hc := limacharlie.NewHiveClient(org)
	data, err := hc.Get(limacharlie.HiveArgs{
		HiveName:     "secret",
		PartitionKey: org.GetOID(),
		Key:          recordName,
	})
	if err != nil {
		return "", err
	}
	value, ok := data.Data["secret"].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("secret not set or is not of type string")
	}

	return data.Data["secret"].(string), nil
}
