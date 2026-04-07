package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
)

// Minimal Swagger 2.0 types for server-side HTML rendering.

type spec struct {
	Info        specInfo               `json:"info"`
	Paths       map[string]pathItem    `json:"paths"`
	Definitions map[string]schemaDef   `json:"definitions"`
	Tags        []specTag              `json:"tags"`
}

type specInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type specTag struct {
	Name string `json:"name"`
}

type pathItem map[string]operation

type operation struct {
	Summary     string              `json:"summary"`
	OperationID string              `json:"operationId"`
	Tags        []string            `json:"tags"`
	Parameters  []parameter         `json:"parameters"`
	Responses   map[string]response `json:"responses"`
}

type parameter struct {
	Name        string     `json:"name"`
	In          string     `json:"in"`
	Required    bool       `json:"required"`
	Type        string     `json:"type"`
	Format      string     `json:"format"`
	Description string     `json:"description"`
	Schema      *schemaRef `json:"schema"`
}

type response struct {
	Description string     `json:"description"`
	Schema      *schemaRef `json:"schema"`
}

type schemaRef struct {
	Ref                  string                `json:"$ref"`
	Type                 string                `json:"type"`
	Format               string                `json:"format"`
	Title                string                `json:"title"`
	Description          string                `json:"description"`
	Enum                 []string              `json:"enum"`
	Properties           map[string]*schemaRef `json:"properties"`
	Items                *schemaRef            `json:"items"`
	AdditionalProperties *schemaRef            `json:"additionalProperties"`
}

type schemaDef struct {
	Type                 string                `json:"type"`
	Description          string                `json:"description"`
	Enum                 []string              `json:"enum"`
	Properties           map[string]*schemaRef `json:"properties"`
	AdditionalProperties *schemaRef            `json:"additionalProperties"`
}

// Template data types.

type endpoint struct {
	Method      string
	Path        string
	Summary     string
	OperationID string
	Tag         string
	Parameters  []param
	Response    string
}

type param struct {
	Name        string
	In          string
	Required    bool
	Type        string
	Description string
}

type definition struct {
	Name        string
	Description string
	Fields      []field
	IsEnum      bool
	EnumValues  []string
}

type field struct {
	Name string
	Type string
	Desc string
}

type tagGroup struct {
	Tag       string
	Endpoints []endpoint
}

func resolveTypeName(s *spec, sr *schemaRef) string {
	if sr == nil {
		return ""
	}
	if sr.Ref != "" {
		parts := strings.Split(sr.Ref, "/")
		return parts[len(parts)-1]
	}
	if sr.Type == "array" && sr.Items != nil {
		return "[]" + resolveTypeName(s, sr.Items)
	}
	if sr.Type == "object" && sr.AdditionalProperties != nil {
		return "map[string]" + resolveTypeName(s, sr.AdditionalProperties)
	}
	if sr.Type == "object" && sr.Properties != nil {
		return "object"
	}
	t := sr.Type
	if sr.Format != "" {
		t += " (" + sr.Format + ")"
	}
	return t
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: swaggerhtml <spec1.json> [spec2.json ...]\n")
		fmt.Fprintf(os.Stderr, "Outputs merged HTML to stdout.\n")
		os.Exit(1)
	}

	// Merge all specs
	merged := &spec{
		Paths:       make(map[string]pathItem),
		Definitions: make(map[string]schemaDef),
	}

	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			os.Exit(1)
		}
		var s spec
		if err := json.Unmarshal(data, &s); err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
			os.Exit(1)
		}
		for k, v := range s.Paths {
			merged.Paths[k] = v
		}
		for k, v := range s.Definitions {
			merged.Definitions[k] = v
		}
		for _, t := range s.Tags {
			merged.Tags = append(merged.Tags, t)
		}
	}

	// Build endpoints
	var eps []endpoint
	for path, item := range merged.Paths {
		for method, op := range item {
			tag := ""
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			var params []param
			for _, p := range op.Parameters {
				typ := p.Type
				if p.Format != "" {
					typ += " (" + p.Format + ")"
				}
				if typ == "" && p.Schema != nil {
					typ = resolveTypeName(merged, p.Schema)
				}
				params = append(params, param{
					Name:        p.Name,
					In:          p.In,
					Required:    p.Required,
					Type:        typ,
					Description: p.Description,
				})
			}
			respType := ""
			if r, ok := op.Responses["200"]; ok && r.Schema != nil {
				respType = resolveTypeName(merged, r.Schema)
			}
			eps = append(eps, endpoint{
				Method:      strings.ToUpper(method),
				Path:        path,
				Summary:     op.Summary,
				OperationID: op.OperationID,
				Tag:         tag,
				Parameters:  params,
				Response:    respType,
			})
		}
	}
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Tag != eps[j].Tag {
			return eps[i].Tag < eps[j].Tag
		}
		return eps[i].Path < eps[j].Path
	})

	// Group by tag
	tagMap := map[string]*tagGroup{}
	var tagOrder []string
	for _, ep := range eps {
		tag := ep.Tag
		if tag == "" {
			tag = "Other"
		}
		if _, ok := tagMap[tag]; !ok {
			tagMap[tag] = &tagGroup{Tag: tag}
			tagOrder = append(tagOrder, tag)
		}
		tagMap[tag].Endpoints = append(tagMap[tag].Endpoints, ep)
	}
	var groups []tagGroup
	for _, t := range tagOrder {
		groups = append(groups, *tagMap[t])
	}

	// Build definitions
	var defs []definition
	for name, d := range merged.Definitions {
		if name == "rpcStatus" || name == "protobufAny" {
			continue
		}
		rd := definition{
			Name:        name,
			Description: d.Description,
		}
		if len(d.Enum) > 0 {
			rd.IsEnum = true
			rd.EnumValues = d.Enum
		} else if d.Properties != nil {
			var fields []field
			for fname, fschema := range d.Properties {
				desc := ""
				if fschema.Title != "" {
					desc = fschema.Title
				} else if fschema.Description != "" {
					desc = fschema.Description
				}
				fields = append(fields, field{
					Name: fname,
					Type: resolveTypeName(merged, fschema),
					Desc: desc,
				})
			}
			sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
			rd.Fields = fields
		}
		defs = append(defs, rd)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })

	// Render
	tmpl := template.Must(template.New("api").Parse(apiTemplate))
	if err := tmpl.Execute(os.Stdout, struct {
		Groups      []tagGroup
		Definitions []definition
	}{
		Groups:      groups,
		Definitions: defs,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "template: %v\n", err)
		os.Exit(1)
	}
}

// Template uses {{.RepoName}} placeholder — anyserver.go wraps this content
// inside the standard page chrome at serve time.
const apiTemplate = `<section class="index-section">
  <h2>API Reference</h2>
  <p>Raw spec: <a href="/api/swagger.json">/api/swagger.json</a></p>
  <p>gRPC reflection is enabled — connect with any gRPC client on the same port.</p>
</section>

{{range .Groups}}
<section class="index-section">
  <h2>{{.Tag}} Service</h2>
  {{range .Endpoints}}
  <div class="api-endpoint">
    <h3><span class="api-method">{{.Method}}</span> <code>{{.Path}}</code></h3>
    {{if .Summary}}<p>{{.Summary}}</p>{{end}}
    {{if .Parameters}}
    <h4>Parameters</h4>
    <table class="file-list">
      <thead><tr><th>Name</th><th>In</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
      <tbody>
      {{range .Parameters}}
      <tr>
        <td><code>{{.Name}}</code></td>
        <td>{{.In}}</td>
        <td><code>{{.Type}}</code></td>
        <td>{{if .Required}}yes{{else}}no{{end}}</td>
        <td>{{.Description}}</td>
      </tr>
      {{end}}
      </tbody>
    </table>
    {{end}}
    {{if .Response}}<p><strong>Response:</strong> <code>{{.Response}}</code></p>{{end}}
  </div>
  {{end}}
</section>
{{end}}

{{if .Definitions}}
<section class="index-section">
  <h2>Schemas</h2>
  {{range .Definitions}}
  <div class="api-schema" id="schema-{{.Name}}">
    <h3><code>{{.Name}}</code></h3>
    {{if .Description}}<p>{{.Description}}</p>{{end}}
    {{if .IsEnum}}
    <p><strong>Enum values:</strong></p>
    <ul>
      {{range .EnumValues}}<li><code>{{.}}</code></li>{{end}}
    </ul>
    {{else if .Fields}}
    <table class="file-list">
      <thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead>
      <tbody>
      {{range .Fields}}
      <tr>
        <td><code>{{.Name}}</code></td>
        <td><code>{{.Type}}</code></td>
        <td>{{.Desc}}</td>
      </tr>
      {{end}}
      </tbody>
    </table>
    {{end}}
  </div>
  {{end}}
</section>
{{end}}
`
