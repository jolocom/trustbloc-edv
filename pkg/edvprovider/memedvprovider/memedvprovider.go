/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memedvprovider

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/trustbloc/edge-core/pkg/storage"
	"github.com/trustbloc/edge-core/pkg/storage/memstore"

	"github.com/trustbloc/edv/pkg/edvprovider"
	"github.com/trustbloc/edv/pkg/restapi/messages"
	"github.com/trustbloc/edv/pkg/restapi/models"
)

const failGetKeyValuePairsFromCoreStoreErrMsg = "failure while getting all key value pairs from core storage: %w"

// ErrQueryingNotSupported is used when an attempt is made to query a vault backed by a memstore.
var ErrQueryingNotSupported = errors.New("querying is not supported by memstore")

// MemEDVProvider represents an in-memory provider with functionality needed for EDV data storage.
// It wraps an edge-core memstore provider with additional functionality that's needed for EDV operations,
// however this additional functionality is not supported in memstore.
type MemEDVProvider struct {
	coreProvider storage.Provider
}

// NewProvider instantiates Provider
func NewProvider() *MemEDVProvider {
	return &MemEDVProvider{coreProvider: memstore.NewProvider()}
}

// CreateStore creates a new store with the given name.
func (m MemEDVProvider) CreateStore(name string) error {
	return m.coreProvider.CreateStore(name)
}

// OpenStore opens an existing store and returns it.
func (m MemEDVProvider) OpenStore(name string) (edvprovider.EDVStore, error) {
	coreStore, err := m.coreProvider.OpenStore(name)
	if err != nil {
		return nil, err
	}

	return &MemEDVStore{coreStore: coreStore}, nil
}

// MemEDVStore represents an in-memory store with functionality needed for EDV data storage.
// It wraps an edge-core in-memory store with additional functionality that's needed for EDV operations.
type MemEDVStore struct {
	coreStore storage.Store
}

// Put stores the given document.
func (m MemEDVStore) Put(document models.EncryptedDocument) error {
	documentBytes, err := json.Marshal(document)
	if err != nil {
		return err
	}

	return m.coreStore.Put(document.ID, documentBytes)
}

// UpsertBulk stores the given documents, creating or updating them as needed.
func (m MemEDVStore) UpsertBulk(documents []models.EncryptedDocument) error {
	if documents == nil {
		return fmt.Errorf("documents array cannot be nil")
	}

	for _, document := range documents {
		err := m.Put(document)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetAll fetches all the documents within this store.
func (m MemEDVStore) GetAll() ([][]byte, error) {
	allKeyValuePairs, err := m.coreStore.GetAll()
	if err != nil {
		return nil, fmt.Errorf(failGetKeyValuePairsFromCoreStoreErrMsg, err)
	}

	var allDocuments [][]byte

	for _, value := range allKeyValuePairs {
		allDocuments = append(allDocuments, value)
	}

	return allDocuments, nil
}

// Get fetches the document associated with the given key.
func (m MemEDVStore) Get(k string) ([]byte, error) {
	return m.coreStore.Get(k)
}

// Update updates the given document
func (m MemEDVStore) Update(newDoc models.EncryptedDocument) error {
	return m.Put(newDoc)
}

// Delete deletes the given document
func (m MemEDVStore) Delete(docID string) error {
	return m.coreStore.Delete(docID)
}

// CreateEDVIndex is not supported in memstore, and calling it will always return an error.
func (m MemEDVStore) CreateEDVIndex() error {
	return edvprovider.ErrIndexingNotSupported
}

// CreateEncryptedDocIDIndex is not supported in memstore, and calling it will always return an error.
func (m MemEDVStore) CreateEncryptedDocIDIndex() error {
	return edvprovider.ErrIndexingNotSupported
}

// Query is not supported in memstore, and calling it will always return an error.
func (m MemEDVStore) Query(*models.Query) ([]models.EncryptedDocument, error) {
	return nil, ErrQueryingNotSupported
}

// StoreDataVaultConfiguration stores the given dataVaultConfiguration and vaultID
func (m MemEDVStore) StoreDataVaultConfiguration(config *models.DataVaultConfiguration, vaultID string) error {
	err := m.checkDuplicateReferenceID(config.ReferenceID)
	if err != nil {
		return fmt.Errorf(messages.CheckDuplicateRefIDFailure, err)
	}

	configEntry := models.DataVaultConfigurationMapping{
		DataVaultConfiguration: *config,
		VaultID:                vaultID,
	}

	configBytes, err := json.Marshal(configEntry)
	if err != nil {
		return fmt.Errorf(messages.FailToMarshalConfig, err)
	}

	err = m.coreStore.Put(fmt.Sprintf("ref:%s", config.ReferenceID), []byte(vaultID))
	if err != nil {
		return err
	}
	return m.coreStore.Put(vaultID, configBytes)
}

// RetrieveDataVaultConfiguration retrieves a DataVaultConfiguration given the vaultID
func (m MemEDVStore) RetrieveDataVaultConfiguration(vaultID string) (*models.DataVaultConfiguration, error) {
	configEntryBytes, err := m.coreStore.Get(vaultID)
	if err != nil {
		return nil, messages.ErrVaultNotFound
	}

	var configEntry models.DataVaultConfigurationMapping
	err = json.Unmarshal(configEntryBytes, &configEntry)
	if err != nil {
		return nil, err
	}
	return &configEntry.DataVaultConfiguration, nil
}

func (m MemEDVStore) checkDuplicateReferenceID(referenceID string) error {
	_, err := m.coreStore.Get(fmt.Sprintf("ref:%s", referenceID))
	if err == nil {
		return messages.ErrDuplicateVault
	}

	if !errors.Is(err, storage.ErrValueNotFound) {
		return err
	}

	return nil
}

// CreateReferenceIDIndex is not supported in memstore, and calling it will always return an error.
func (m MemEDVStore) CreateReferenceIDIndex() error {
	return edvprovider.ErrIndexingNotSupported
}
