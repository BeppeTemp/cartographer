package mcpserver

import (
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/openapi"
)

// apiErrorEnvelope is the body of every failure.
type apiErrorEnvelope struct {
	Error uiError `json:"error"`
}

// apiErrorCodes are the machine codes of uiError.Code.
var apiErrorCodes = []string{uiCodeInvalidRequest, uiCodeNotFound, uiCodeMethodNotAllow, uiCodeInternal}

const apiOpenAPIPath = "/openapi.json"

var (
	openAPIOnce sync.Once
	openAPIDoc  []byte
	openAPIErr  error
)

// OpenAPIDocument is the OpenAPI 3.1 document of /api/v1, generated from the
// route table: the stable and preview routes, plus the document itself. It
// holds nothing about any KB or caller (it is served without a token). The
// committed copy docs/openapi.json is the same bytes (`make openapi`).
func OpenAPIDocument() ([]byte, error) {
	openAPIOnce.Do(func() { openAPIDoc, openAPIErr = buildOpenAPIDocument() })
	return openAPIDoc, openAPIErr
}

func buildOpenAPIDocument() ([]byte, error) {
	ops := []openapi.Operation{{
		ID: "openapi", Path: APIPrefix + apiOpenAPIPath, Summary: "This document", Stability: tierStable.String(),
		Public: true,
	}}
	for _, rt := range apiRoutes {
		if rt.Tier&apiPublicTiers == 0 {
			continue
		}
		op := openapi.Operation{
			ID: operationID(rt), Path: APIPrefix + rt.Pattern, Summary: rt.Summary, Stability: rt.Tier.String(),
			Errors: append([]int{http.StatusUnauthorized}, rt.Errors...),
		}
		if rt.Response != nil {
			op.Response = reflect.TypeOf(rt.Response)
		}
		for _, ev := range rt.Events {
			op.Events = append(op.Events, openapi.Event{Name: ev.Name, Type: reflect.TypeOf(ev.Data)})
		}
		if rt.kbScoped() {
			op.Params = append(op.Params, openapi.Param{Name: "kb", In: "path", Description: "KB name", Required: true})
			op.Errors = append(op.Errors, http.StatusNotFound)
		}
		for _, q := range rt.Query {
			op.Params = append(op.Params, openapi.Param{Name: q.Name, In: "query", Description: q.Description, Type: q.Type, Required: q.Required})
		}
		ops = append(ops, op)
	}
	return openapi.Build(openapi.Document{
		Title:       "Cartographer public API",
		Description: "A read-only API over the knowledge bases a bearer token may see. Writes are MCP tools. Routes marked x-stability preview may change in a minor release.",
		Version:     "1",
		Error:       reflect.TypeOf(apiErrorEnvelope{}),
		ErrorCodes:  apiErrorCodes,
		Ops:         ops,
	})
}

// operationID is the route's non-variable segments in camel case:
// /kbs/{kb}/maintenance/review is kbsMaintenanceReview.
func operationID(rt apiRoute) string {
	id := ""
	for _, seg := range rt.segments {
		if seg == "{kb}" {
			continue
		}
		if id != "" {
			seg = strings.ToUpper(seg[:1]) + seg[1:]
		}
		id += seg
	}
	return id
}

// serveOpenAPI answers GET /api/v1/openapi.json. It never reads the principal.
func serveOpenAPI(w http.ResponseWriter) {
	doc, err := OpenAPIDocument()
	if err != nil {
		writeUIInternal(w, "openapi", err)
		return
	}
	writeUIHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}
