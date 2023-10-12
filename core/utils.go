package core

import (
	"fmt"
	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"path"
	"strings"
	"sync"
)

var (
	secretCache = make(map[string]string)
	cacheMutex  sync.RWMutex
)

func UseSecretValue(key string, org *limacharlie.Organization, fn func(val string) error) error {
	var err error
	var apiKey string
	var secretName string
	if strings.Contains(key, "hive://secret/") {
		for i := 0; i < 2; i++ { // max of two tries
			// Try to get secret from cache
			apiKey, secretName, err = GetSecret(key, org)
			if err != nil {
				return err
			}

			err = fn(apiKey)
			if err == nil {
				return nil
			}

			if i == 1 { // no need to clear cache this is 2nd call
				return err
			}

			// Clear the cache to ensure the next iteration fetches the secret from Hive again
			deleteSecretCache(secretName)

		}
		return fmt.Errorf("secrets function failed for org: %s err: %v", org.GetOID(), err)
	}

	// no retry logic if actual key passed
	if err := fn(apiKey); err != nil {
		return err
	}
	return nil
}

func GetSecret(key string, org *limacharlie.Organization) (string, string, error) {
	apiKey := key
	var exists bool
	var secretName string
	if strings.Contains(key, "hive://secret/") {
		secretName = path.Base(key)
		// Try to get secret from cache
		apiKey, exists = getSecretFromCache(secretName)
		if !exists {
			var err error
			apiKey, err = getSecretFromHive(secretName, org)
			if err != nil {
				return "", "", err
			}

			// ensure to update local cache
			setSecretCache(secretName, apiKey)
		}
	}

	return apiKey, secretName, nil
}

func setSecretCache(secretName, apiKey string) {
	cacheMutex.Lock()
	secretCache[secretName] = apiKey
	cacheMutex.Unlock()
}

func getSecretFromCache(secretName string) (string, bool) {
	cacheMutex.RLock()
	val, exists := secretCache[secretName]
	cacheMutex.RUnlock()
	return val, exists
}

func deleteSecretCache(secretName string) {
	cacheMutex.Lock()
	delete(secretCache, secretName)
	cacheMutex.Unlock()
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
