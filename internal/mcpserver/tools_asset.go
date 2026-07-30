package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func decodeAssetContent(content, encoding string) ([]byte, error) {
	if encoding == "" || encoding == "text" {
		return []byte(content), nil
	}
	if encoding != "base64" {
		return nil, fmt.Errorf("encoding must be text or base64")
	}
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("encoding base64: invalid content")
	}
	return data, nil
}

func assetResult(v interface{}) ToolResult {
	out, _ := json.MarshalIndent(v, "", "  ")
	return textResult(string(out))
}

// --- asset_read ---

func toolAssetRead(k *kb.KB) Tool {
	return Tool{
		Name:        "asset_read",
		ReadOnly:    true,
		Description: "Reads a non-Markdown asset owned by an expanded concept. Returns raw content, encoding, sha256, size, and executable mode; use sha256 as if_match when changing or deleting the asset.",
		InputSchema: json.RawMessage(`{"type":"object","required":["concept_id","path"],"properties":{"concept_id":{"type":"string"},"path":{"type":"string"},"encoding":{"type":"string","enum":["text","base64"]}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				ConceptID string `json:"concept_id"`
				Path      string `json:"path"`
				Encoding  string `json:"encoding"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if p.Encoding != "" && p.Encoding != "text" && p.Encoding != "base64" {
				return errorResult("encoding must be text or base64"), nil
			}
			data, entry, err := k.ReadAsset(okf.ConceptID(p.ConceptID), p.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			encoding, content := "text", string(data)
			if p.Encoding == "base64" || !utf8.Valid(data) {
				encoding, content = "base64", base64.StdEncoding.EncodeToString(data)
			}
			return assetResult(map[string]interface{}{"path": entry.Path, "content": content, "encoding": encoding, "sha256": entry.SHA256, "size": entry.Size, "executable": entry.Executable}), nil
		},
	}
}

// --- asset_list ---

func toolAssetList(k *kb.KB) Tool {
	return Tool{
		Name:        "asset_list",
		ReadOnly:    true,
		Description: "Lists every non-Markdown regular asset owned by an expanded concept, including its path, size, sha256, and executable mode.",
		InputSchema: json.RawMessage(`{"type":"object","required":["concept_id"],"properties":{"concept_id":{"type":"string"}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				ConceptID string `json:"concept_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			entries, err := k.ListAssets(okf.ConceptID(p.ConceptID))
			if err != nil {
				return errorResult(err.Error()), nil
			}
			return assetResult(entries), nil
		},
	}
}

// --- asset_write ---

func toolAssetWrite(k *kb.KB) Tool {
	return Tool{
		Name:        "asset_write",
		Description: "Creates or updates a non-Markdown asset inside an expanded concept. New assets must omit if_match; overwrites require the current raw-byte sha256 from asset_read or asset_list. Content may be text or base64. executable is tri-state: omitted creates non-executable files and preserves an existing mode; true or false explicitly sets the mode.",
		InputSchema: json.RawMessage(`{"type":"object","required":["concept_id","path","content"],"properties":{"concept_id":{"type":"string"},"path":{"type":"string"},"content":{"type":"string"},"encoding":{"type":"string","enum":["text","base64"]},"executable":{"type":"boolean","description":"Tri-state: omit to create non-executable or preserve an overwrite; true/false explicitly sets executable mode."},"if_match":{"type":"string"}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				ConceptID  string `json:"concept_id"`
				Path       string `json:"path"`
				Content    string `json:"content"`
				Encoding   string `json:"encoding"`
				Executable *bool  `json:"executable"`
				IfMatch    string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			data, err := decodeAssetContent(p.Content, p.Encoding)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			entry, err := k.WriteAsset(okf.ConceptID(p.ConceptID), p.Path, data, p.IfMatch, p.Executable)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			return assetResult(entry), nil
		},
	}
}

// --- asset_delete ---

func toolAssetDelete(k *kb.KB) Tool {
	return Tool{
		Name:        "asset_delete",
		Description: "Permanently deletes a non-Markdown asset from an expanded concept. if_match is mandatory and must be the current raw-byte sha256 returned by asset_read or asset_list.",
		InputSchema: json.RawMessage(`{"type":"object","required":["concept_id","path","if_match"],"properties":{"concept_id":{"type":"string"},"path":{"type":"string"},"if_match":{"type":"string"}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				ConceptID string `json:"concept_id"`
				Path      string `json:"path"`
				IfMatch   string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if err := k.DeleteAsset(okf.ConceptID(p.ConceptID), p.Path, p.IfMatch); err != nil {
				return errorResult(err.Error()), nil
			}
			return assetResult(map[string]string{"path": p.Path, "status": "deleted"}), nil
		},
	}
}
