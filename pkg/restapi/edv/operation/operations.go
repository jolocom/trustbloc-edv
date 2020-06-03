/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package operation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/btcsuite/btcutil/base58"
	"github.com/gorilla/mux"
	log "github.com/trustbloc/edge-core/pkg/log"
	"github.com/trustbloc/edge-core/pkg/storage"

	"github.com/trustbloc/edv/pkg/edvprovider"
	"github.com/trustbloc/edv/pkg/internal/common/support"
	"github.com/trustbloc/edv/pkg/restapi/edv/edverrors"
	"github.com/trustbloc/edv/pkg/restapi/edv/models"
)

const (
	edvCommonEndpointPathRoot = "/encrypted-data-vaults"
	vaultIDPathVariable       = "vaultID"
	docIDPathVariable         = "docID"

	createVaultEndpoint    = edvCommonEndpointPathRoot
	queryVaultEndpoint     = edvCommonEndpointPathRoot + "/{" + vaultIDPathVariable + "}/queries"
	createDocumentEndpoint = edvCommonEndpointPathRoot + "/{" + vaultIDPathVariable + "}/documents"
	readDocumentEndpoint   = edvCommonEndpointPathRoot + "/{" + vaultIDPathVariable + "}/documents/{" +
		docIDPathVariable + "}"
	logSpecEndpoint = edvCommonEndpointPathRoot + "/logspec"

	setLogLevelSuccessMsg = "Successfully set log level(s)."
	invalidLogSpecMsg     = `Invalid log spec. It needs to be in the following format: ` +
		`ModuleName1=Level1:ModuleName2=Level2:ModuleNameN=LevelN:AllOtherModuleDefaultLevel
Valid log levels: critical,error,warn,info,debug`
	getLogLevelPrepareErrMsg = "Failure while preparing log level response: %s"
)

var logger = log.New("restapi")

// Handler http handler for each controller API endpoint
type Handler interface {
	Path() string
	Method() string
	Handle() http.HandlerFunc
}

type stringBuilder interface {
	Write(p []byte) (int, error)
	String() string
	Reset()
}

// New returns a new EDV operations instance.
// If dbPrefix is blank, then no prefixing will be done to the vault IDs.
func New(provider edvprovider.EDVProvider) *Operation {
	svc := &Operation{
		vaultCollection: VaultCollection{
			provider: provider,
		},
		getLogSpecResponse: &strings.Builder{}}
	svc.registerHandler()

	return svc
}

// Operation defines handlers for EDV service
type Operation struct {
	handlers           []Handler
	vaultCollection    VaultCollection
	getLogSpecResponse stringBuilder
}

// VaultCollection represents EDV storage.
type VaultCollection struct {
	provider edvprovider.EDVProvider
}

func (c *Operation) createDataVaultHandler(rw http.ResponseWriter, req *http.Request) {
	config := models.DataVaultConfiguration{}

	err := json.NewDecoder(req.Body).Decode(&config)

	blankReferenceIDProvided := err == nil && config.ReferenceID == ""

	if err != nil || blankReferenceIDProvided {
		rw.WriteHeader(http.StatusBadRequest)

		var errMsg string
		if blankReferenceIDProvided {
			errMsg = "referenceId can't be blank"
		} else {
			errMsg = err.Error()
		}

		_, err = rw.Write([]byte(errMsg))
		if err != nil {
			logger.Errorf("Failed to write response for data vault creation failure due to the provided"+
				" data vault configuration: %s", err.Error())
		}

		return
	}

	err = c.vaultCollection.createDataVault(config.ReferenceID)
	if err != nil {
		if err == edverrors.ErrDuplicateVault {
			rw.WriteHeader(http.StatusConflict)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		_, err = rw.Write([]byte(fmt.Sprintf("Data vault creation failed: %s", err)))
		if err != nil {
			logger.Errorf("Failed to write response for data vault creation failure: %s", err.Error())
		}

		return
	}

	urlEncodedReferenceID := url.PathEscape(config.ReferenceID)

	rw.Header().Set("Location", req.Host+"/encrypted-data-vaults/"+urlEncodedReferenceID)
	rw.WriteHeader(http.StatusCreated)
}

func (c *Operation) queryVaultHandler(rw http.ResponseWriter, req *http.Request) {
	incomingQuery := models.Query{}

	err := json.NewDecoder(req.Body).Decode(&incomingQuery)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf(edverrors.QueryVaultFailureToWriteFailureResponseErrMsg, err.Error())
		}

		return
	}

	vaultID, success := unescapePathVar(vaultIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	matchingDocumentIDs, err := c.vaultCollection.queryVault(vaultID, &incomingQuery)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf(edverrors.QueryVaultFailureToWriteFailureResponseErrMsg, err.Error())
		}

		return
	}

	fullDocumentURLs := convertToFullDocumentURLs(matchingDocumentIDs, vaultID, req)

	sendQueryResponse(rw, fullDocumentURLs)
}

func (c *Operation) createDocumentHandler(rw http.ResponseWriter, req *http.Request) {
	incomingDocument := models.EncryptedDocument{}

	err := json.NewDecoder(req.Body).Decode(&incomingDocument)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf("Failed to write response for document creation failure: %s", err.Error())
		}

		return
	}

	vaultID, success := unescapePathVar(vaultIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	err = c.vaultCollection.createDocument(vaultID, incomingDocument)
	if err != nil {
		if err == edverrors.ErrDuplicateDocument {
			rw.WriteHeader(http.StatusConflict)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf(
				"Failed to write response for document creation failure: %s", err.Error())
		}

		return
	}

	rw.Header().Set("Location", req.Host+"/encrypted-data-vaults/"+
		url.PathEscape(vaultID)+"/documents/"+url.PathEscape(incomingDocument.ID))
	rw.WriteHeader(http.StatusCreated)
}

func (c *Operation) readDocumentHandler(rw http.ResponseWriter, req *http.Request) {
	vaultID, success := unescapePathVar(vaultIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	docID, success := unescapePathVar(docIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	documentBytes, err := c.vaultCollection.readDocument(vaultID, docID)
	if err != nil {
		if err == edverrors.ErrDocumentNotFound || err == edverrors.ErrVaultNotFound {
			rw.WriteHeader(http.StatusNotFound)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf("Failed to write response for document retrieval failure: %s", err.Error())
		}

		return
	}

	_, err = rw.Write(documentBytes)
	if err != nil {
		logger.Errorf("Failed to write response for document retrieval success: %s", err.Error())
	}
}

func (vc *VaultCollection) createDataVault(vaultID string) error {
	err := vc.provider.CreateStore(vaultID)
	if err == storage.ErrDuplicateStore {
		return edverrors.ErrDuplicateVault
	}

	store, err := vc.provider.OpenStore(vaultID)
	if err != nil {
		return err
	}

	err = store.CreateEDVIndex()
	if err != nil {
		if err == edvprovider.ErrIndexingNotSupported { // Allow the EDV to still operate without index support
			return nil
		}

		return err
	}

	return nil
}

func (vc *VaultCollection) createDocument(vaultID string, document models.EncryptedDocument) error {
	store, err := vc.provider.OpenStore(vaultID)
	if err != nil {
		if err == storage.ErrStoreNotFound {
			return edverrors.ErrVaultNotFound
		}

		return err
	}

	if encodingErr := checkIfBase58Encoded128BitValue(document.ID); encodingErr != nil {
		return encodingErr
	}

	// The Create Document API call should not overwrite an existing document.
	// So we first check to make sure there is not already a document associated with the id.
	// If there is, we send back an error.
	_, err = store.Get(document.ID)
	if err == nil {
		return edverrors.ErrDuplicateDocument
	}

	if err != storage.ErrValueNotFound {
		return err
	}

	return store.Put(document)
}

func (vc *VaultCollection) readDocument(vaultID, docID string) ([]byte, error) {
	store, err := vc.provider.OpenStore(vaultID)
	if err != nil {
		if err == storage.ErrStoreNotFound {
			return nil, edverrors.ErrVaultNotFound
		}

		return nil, err
	}

	documentBytes, err := store.Get(docID)
	if err != nil {
		if err == storage.ErrValueNotFound {
			return nil, edverrors.ErrDocumentNotFound
		}

		return nil, err
	}

	return documentBytes, err
}

func (vc *VaultCollection) queryVault(vaultID string, query *models.Query) ([]string, error) {
	store, err := vc.provider.OpenStore(vaultID)
	if err != nil {
		if err == storage.ErrStoreNotFound {
			return nil, edverrors.ErrVaultNotFound
		}

		return nil, err
	}

	return store.Query(query)
}

// This function can't tell if the value before being encoded was precisely 128 bits long.
// This is because the byte58.decode function returns an array of bytes, not just a string of bits.
// So the closest I can do is see if the decoded byte array is 16 bytes long,
// however this means that if the original value was 121 bits to 127 bits long it'll still be accepted.
func checkIfBase58Encoded128BitValue(id string) error {
	decodedBytes := base58.Decode(id)
	if len(decodedBytes) == 0 {
		return edverrors.ErrNotBase58Encoded
	}

	if len(decodedBytes) != 16 {
		return edverrors.ErrNot128BitValue
	}

	return nil
}

func sendQueryResponse(rw http.ResponseWriter, matchingDocumentIDs []string) {
	if matchingDocumentIDs == nil {
		_, err := rw.Write([]byte("no matching documents found"))
		if err != nil {
			logger.Errorf(edverrors.QueryVaultFailureToWriteSuccessResponseErrMsg, err.Error())
		}

		return
	}

	matchingDocumentIDsBytes, err := json.Marshal(matchingDocumentIDs)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			logger.Errorf(edverrors.QueryVaultFailureToWriteFailureResponseErrMsg, err.Error())
		}

		return
	}

	_, err = rw.Write(matchingDocumentIDsBytes)
	if err != nil {
		logger.Errorf(edverrors.QueryVaultFailureToWriteSuccessResponseErrMsg, err.Error())
	}
}

type moduleLevelPair struct {
	module   string
	logLevel log.Level
}

// Note that this will not work properly if a module name contains an '=' character.
func (c *Operation) logSpecPutHandler(rw http.ResponseWriter, req *http.Request) {
	incomingLogSpec := models.LogSpec{}

	err := json.NewDecoder(req.Body).Decode(&incomingLogSpec)
	if err != nil {
		writeInvalidLogSpec(rw)
		return
	}

	logLevelByModule := strings.Split(incomingLogSpec.Spec, ":")

	defaultLogLevel := log.Level(-1)

	var moduleLevelPairs []moduleLevelPair

	for _, logLevelByModulePart := range logLevelByModule {
		if strings.Contains(logLevelByModulePart, "=") {
			moduleAndLevelPair := strings.Split(logLevelByModulePart, "=")

			logLevel, parseErr := log.ParseLevel(moduleAndLevelPair[1])
			if parseErr != nil {
				writeInvalidLogSpec(rw)
				return
			}

			moduleLevelPairs = append(moduleLevelPairs,
				moduleLevelPair{moduleAndLevelPair[0], logLevel})
		} else {
			if defaultLogLevel != -1 {
				// The given log spec is formatted incorrectly; it contains multiple default values.
				writeInvalidLogSpec(rw)
				return
			}
			var parseErr error

			defaultLogLevel, parseErr = log.ParseLevel(logLevelByModulePart)
			if parseErr != nil {
				writeInvalidLogSpec(rw)
				return
			}
		}
	}

	if defaultLogLevel != -1 {
		log.SetLevel("", defaultLogLevel)
	}

	for _, moduleLevelPair := range moduleLevelPairs {
		log.SetLevel(moduleLevelPair.module, moduleLevelPair.logLevel)
	}

	_, err = rw.Write([]byte(setLogLevelSuccessMsg))
	if err != nil {
		logger.Errorf(setLogLevelSuccessMsg+" Failed to write response to sender: %s", err)
	}
}

func (c *Operation) logSpecGetHandler(rw http.ResponseWriter, _ *http.Request) {
	logLevels := log.GetAllLevels()

	var defaultDebugLevel string

	c.getLogSpecResponse.Reset()

	for module, level := range logLevels {
		if module == "" {
			defaultDebugLevel = log.ParseString(level)
		} else {
			_, err := c.getLogSpecResponse.Write([]byte(module + `=` + log.ParseString(level) + ":"))
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				logger.Errorf(getLogLevelPrepareErrMsg, err)

				return
			}
		}
	}

	_, err := c.getLogSpecResponse.Write([]byte(defaultDebugLevel))
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		logger.Errorf(getLogLevelPrepareErrMsg, err)

		return
	}

	_, err = rw.Write([]byte(c.getLogSpecResponse.String()))
	if err != nil {
		logger.Errorf("Successfully got log spec, but failed to write response to sender: %s", err)
	}
}

func writeInvalidLogSpec(rw http.ResponseWriter) {
	rw.WriteHeader(http.StatusBadRequest)

	_, err := rw.Write([]byte(invalidLogSpecMsg))
	if err != nil {
		logger.Errorf("Invalid log spec. Failed to write message to sender: %s",
			err.Error())
	}
}

// registerHandler register handlers to be exposed from this service as REST API endpoints
func (c *Operation) registerHandler() {
	// Add more protocol endpoints here to expose them as controller API endpoints
	c.handlers = []Handler{
		support.NewHTTPHandler(createVaultEndpoint, http.MethodPost, c.createDataVaultHandler),
		support.NewHTTPHandler(queryVaultEndpoint, http.MethodPost, c.queryVaultHandler),
		support.NewHTTPHandler(createDocumentEndpoint, http.MethodPost, c.createDocumentHandler),
		support.NewHTTPHandler(readDocumentEndpoint, http.MethodGet, c.readDocumentHandler),
		support.NewHTTPHandler(logSpecEndpoint, http.MethodPut, c.logSpecPutHandler),
		support.NewHTTPHandler(logSpecEndpoint, http.MethodGet, c.logSpecGetHandler),
	}
}

// GetRESTHandlers get all controller API handler available for this service
func (c *Operation) GetRESTHandlers() []Handler {
	return c.handlers
}

// Unescapes the given path variable from the vars map and writes a response if any failure occurs.
// Returns the unescaped version of the path variable and a bool indicating whether the unescaping was successful.
func unescapePathVar(pathVar string, vars map[string]string, rw http.ResponseWriter) (string, bool) {
	unescapedPathVar, err := url.PathUnescape(vars[pathVar])
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)

		_, err = rw.Write([]byte(fmt.Sprintf("unable to escape %s path variable: %s", pathVar, err.Error())))
		if err != nil {
			logger.Errorf("Failed to write response for %s unescaping failure: %s", pathVar, err.Error())
		}

		return "", false
	}

	return unescapedPathVar, true
}

func convertToFullDocumentURLs(documentIDs []string, vaultID string, req *http.Request) []string {
	fullDocumentURLs := make([]string, len(documentIDs))

	for i, matchingDocumentID := range documentIDs {
		fullDocumentURLs[i] = req.Host + "/encrypted-data-vaults/" +
			url.PathEscape(vaultID) + "/documents/" + url.PathEscape(matchingDocumentID)
	}

	return fullDocumentURLs
}
