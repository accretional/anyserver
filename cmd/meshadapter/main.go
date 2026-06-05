// Command meshadapter turns the KVQ mesh's service definitions into a single
// Swagger 2.0 JSON spec that anyserver's swaggerhtml renderer can display.
//
// It ingests two kinds of input, the same currency the KVQ TypeRegistry speaks:
//
//  1. Curated protobuf FileDescriptorSets (.pb files, --include_imports output).
//     These are intentionally a SMALL curated set to stay within memory limits.
//  2. Live gRPC server reflection from a running mesh backend (e.g. chromerpc on
//     :50051, or a regserver fronting the TypeRegistry). This is how anyserver
//     fronts the running mesh: every reflection-enabled backend contributes its
//     services/types to the one UI.
//
// Output: a merged swagger.json. Each gRPC service becomes a Swagger tag; each
// method becomes a POST path /<pkg.Service>/<Method>; each message becomes a
// definition. swaggerhtml groups by tag, so the API Reference page shows every
// service and every type in the mesh on one page.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	refv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ---- Swagger 2.0 (minimal, matches what swaggerhtml consumes) ----

type swSpec struct {
	Swagger     string                 `json:"swagger"`
	Info        swInfo                 `json:"info"`
	Paths       map[string]swPathItem  `json:"paths"`
	Definitions map[string]swSchema    `json:"definitions"`
	Tags        []swTag                `json:"tags"`
}
type swInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}
type swTag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type swPathItem map[string]swOp
type swOp struct {
	Summary     string                `json:"summary,omitempty"`
	OperationID string                `json:"operationId"`
	Tags        []string              `json:"tags"`
	Parameters  []swParam             `json:"parameters,omitempty"`
	Responses   map[string]swResponse `json:"responses"`
}
type swParam struct {
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Required    bool      `json:"required"`
	Description string    `json:"description,omitempty"`
	Schema      *swSchema `json:"schema,omitempty"`
}
type swResponse struct {
	Description string    `json:"description"`
	Schema      *swSchema `json:"schema,omitempty"`
}
type swSchema struct {
	Ref         string               `json:"$ref,omitempty"`
	Type        string               `json:"type,omitempty"`
	Format      string               `json:"format,omitempty"`
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	Enum        []string             `json:"enum,omitempty"`
	Properties  map[string]*swSchema `json:"properties,omitempty"`
	Items       *swSchema            `json:"items,omitempty"`
}

func main() {
	out := flag.String("out", "/tmp/mesh.swagger.json", "output swagger json path")
	reflectAddrs := flag.String("reflect", "", "comma-separated gRPC reflection addresses (e.g. localhost:50051)")
	title := flag.String("title", "KVQ Service Mesh", "spec title")
	flag.Parse()

	// Collect FileDescriptorProtos from all sources into one registry.
	fdps := map[string]*descriptorpb.FileDescriptorProto{}

	// 1. Curated descriptor-set files (positional args).
	for _, path := range flag.Args() {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", path, err)
		}
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(b, &set); err != nil {
			log.Fatalf("parse %s: %v", path, err)
		}
		for _, fd := range set.GetFile() {
			if fd.GetName() != "" {
				fdps[fd.GetName()] = fd
			}
		}
		log.Printf("loaded %s: %d files", path, len(set.GetFile()))
	}

	// 2. Live reflection backends (the running mesh).
	var reflectServices []string // fully-qualified service names sourced from reflection
	if *reflectAddrs != "" {
		for _, addr := range strings.Split(*reflectAddrs, ",") {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			svcs, files, err := pullReflection(addr)
			if err != nil {
				log.Printf("WARN reflection %s: %v (skipping)", addr, err)
				continue
			}
			for _, fd := range files {
				if fd.GetName() != "" {
					if _, ok := fdps[fd.GetName()]; !ok {
						fdps[fd.GetName()] = fd
					}
				}
			}
			reflectServices = append(reflectServices, svcs...)
			log.Printf("reflection %s: %d services, %d files", addr, len(svcs), len(files))
		}
	}

	// Build a protoregistry.Files so we can resolve cross-file type refs.
	set := &descriptorpb.FileDescriptorSet{}
	for _, fd := range fdps {
		set.File = append(set.File, fd)
	}
	files, err := protodesc.NewFiles(sortAndDedupForDeps(set))
	if err != nil {
		// Some curated sets may be missing imports; fall back to lenient per-file.
		log.Printf("WARN strict resolve failed (%v); using lenient resolver", err)
		files = lenientFiles(set)
	}

	spec := &swSpec{
		Swagger:     "2.0",
		Info:        swInfo{Title: *title, Version: "mesh"},
		Paths:       map[string]swPathItem{},
		Definitions: map[string]swSchema{},
	}

	reflectSet := map[string]bool{}
	for _, s := range reflectServices {
		reflectSet[s] = true
	}

	tagSeen := map[string]bool{}
	msgSeen := map[string]bool{}

	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			svc := svcs.Get(i)
			full := string(svc.FullName())
			tag := full
			if !tagSeen[tag] {
				desc := "from descriptor set"
				if reflectSet[full] {
					desc = "LIVE via gRPC reflection"
				}
				spec.Tags = append(spec.Tags, swTag{Name: tag, Description: desc})
				tagSeen[tag] = true
			}
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				m := methods.Get(j)
				inName := string(m.Input().FullName())
				outName := string(m.Output().FullName())
				path := "/" + full + "/" + string(m.Name())
				summary := fmt.Sprintf("%s(%s) returns %s", m.Name(), shortName(inName), shortName(outName))
				if m.IsStreamingClient() || m.IsStreamingServer() {
					summary += " [streaming]"
				}
				spec.Paths[path] = swPathItem{
					"post": swOp{
						Summary:     summary,
						OperationID: full + "." + string(m.Name()),
						Tags:        []string{tag},
						Parameters: []swParam{{
							Name:     "body",
							In:       "body",
							Required: true,
							Schema:   &swSchema{Ref: "#/definitions/" + defKey(inName)},
						}},
						Responses: map[string]swResponse{
							"200": {Description: "OK", Schema: &swSchema{Ref: "#/definitions/" + defKey(outName)}},
						},
					},
				}
				collectMessage(spec, m.Input(), msgSeen)
				collectMessage(spec, m.Output(), msgSeen)
			}
		}
		return true
	})

	sort.Slice(spec.Tags, func(i, j int) bool { return spec.Tags[i].Name < spec.Tags[j].Name })

	data, _ := json.MarshalIndent(spec, "", "  ")
	if err := os.WriteFile(*out, data, 0644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
	log.Printf("wrote %s: %d services (tags), %d paths, %d definitions",
		*out, len(spec.Tags), len(spec.Paths), len(spec.Definitions))
}

func shortName(full string) string {
	i := strings.LastIndex(full, ".")
	if i < 0 {
		return full
	}
	return full[i+1:]
}

// defKey turns a fully-qualified message name into a stable definition key.
func defKey(full string) string {
	return strings.ReplaceAll(full, ".", "")
}

func collectMessage(spec *swSpec, md protoreflect.MessageDescriptor, seen map[string]bool) {
	full := string(md.FullName())
	key := defKey(full)
	if seen[key] {
		return
	}
	seen[key] = true

	def := swSchema{Type: "object", Title: full, Properties: map[string]*swSchema{}}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		def.Properties[string(f.Name())] = fieldSchema(spec, f, seen)
	}
	spec.Definitions[key] = def
}

func fieldSchema(spec *swSpec, f protoreflect.FieldDescriptor, seen map[string]bool) *swSchema {
	base := scalarSchema(spec, f, seen)
	if f.IsList() {
		return &swSchema{Type: "array", Items: base}
	}
	if f.IsMap() {
		// represent map as object with additionalProperties-ish title
		return &swSchema{Type: "object", Title: "map"}
	}
	return base
}

func scalarSchema(spec *swSpec, f protoreflect.FieldDescriptor, seen map[string]bool) *swSchema {
	switch f.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		md := f.Message()
		collectMessage(spec, md, seen)
		return &swSchema{Ref: "#/definitions/" + defKey(string(md.FullName()))}
	case protoreflect.EnumKind:
		ed := f.Enum()
		var vals []string
		vs := ed.Values()
		for i := 0; i < vs.Len(); i++ {
			vals = append(vals, string(vs.Get(i).Name()))
		}
		return &swSchema{Type: "string", Enum: vals, Title: string(ed.FullName())}
	case protoreflect.BoolKind:
		return &swSchema{Type: "boolean"}
	case protoreflect.StringKind:
		return &swSchema{Type: "string"}
	case protoreflect.BytesKind:
		return &swSchema{Type: "string", Format: "byte"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return &swSchema{Type: "number", Format: f.Kind().String()}
	default:
		return &swSchema{Type: "integer", Format: f.Kind().String()}
	}
}

// ---- gRPC reflection client (v1) ----

func pullReflection(addr string) (services []string, files []*descriptorpb.FileDescriptorProto, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20)))
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	rc := refv1.NewServerReflectionClient(conn)
	stream, err := rc.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, nil, err
	}

	// List services.
	if err := stream.Send(&refv1.ServerReflectionRequest{
		MessageRequest: &refv1.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, nil, err
	}
	var svcNames []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		if s.GetName() == "grpc.reflection.v1.ServerReflection" ||
			s.GetName() == "grpc.reflection.v1alpha.ServerReflection" {
			continue
		}
		svcNames = append(svcNames, s.GetName())
	}

	// Fetch the file descriptor for each service symbol (transitive deps included).
	fileSeen := map[string]bool{}
	var fds []*descriptorpb.FileDescriptorProto
	for _, name := range svcNames {
		if err := stream.Send(&refv1.ServerReflectionRequest{
			MessageRequest: &refv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name},
		}); err != nil {
			return nil, nil, err
		}
		r, err := stream.Recv()
		if err != nil {
			return nil, nil, err
		}
		for _, raw := range r.GetFileDescriptorResponse().GetFileDescriptorProto() {
			var fd descriptorpb.FileDescriptorProto
			if err := proto.Unmarshal(raw, &fd); err != nil {
				continue
			}
			if fd.GetName() == "" || fileSeen[fd.GetName()] {
				continue
			}
			fileSeen[fd.GetName()] = true
			fds = append(fds, &fd)
		}
	}
	return svcNames, fds, nil
}

// ---- descriptor resolution helpers ----

// sortAndDedupForDeps orders files so dependencies precede dependents (best
// effort) and drops dupes, which protodesc.NewFiles requires.
func sortAndDedupForDeps(set *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	byName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fd := range set.File {
		byName[fd.GetName()] = fd
	}
	var ordered []*descriptorpb.FileDescriptorProto
	visited := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		fd := byName[name]
		if fd == nil {
			visited[name] = true
			return
		}
		visited[name] = true
		for _, dep := range fd.GetDependency() {
			visit(dep)
		}
		ordered = append(ordered, fd)
	}
	var names []string
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		visit(n)
	}
	return &descriptorpb.FileDescriptorSet{File: ordered}
}

// lenientFiles registers files one at a time, skipping any whose deps cannot be
// resolved, so a missing import in one curated set does not sink the whole UI.
func lenientFiles(set *descriptorpb.FileDescriptorSet) *protoregistry.Files {
	files := &protoregistry.Files{}
	byName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fd := range set.File {
		byName[fd.GetName()] = fd
	}
	ordered := sortAndDedupForDeps(set).File
	for _, fd := range ordered {
		// Resolve already-registered deps; skip files referencing unknown deps.
		ok := true
		for _, dep := range fd.GetDependency() {
			if _, e := files.FindFileByPath(dep); e != nil {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		f, err := protodesc.NewFile(fd, files)
		if err != nil {
			continue
		}
		if err := files.RegisterFile(f); err != nil {
			continue
		}
	}
	return files
}
