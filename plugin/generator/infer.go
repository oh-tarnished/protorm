package generator

// infer.go contains proto annotation accessors and comment/name utilities used
// by build.go. Type inference lives in the types package.

import (
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"

	"github.com/oh-tarnished/protorm/protorm/protormpbv1"
)

// colName returns cOpts.name if set, otherwise the proto field name (already snake_case).
func colName(f *protogen.Field, opts *protormpbv1.ColOptions) string {
	if n := opts.GetColumn(); n != "" {
		return n
	}
	return string(f.Desc.Name())
}

// Annotation accessors. Each returns a safe non-nil default when its extension is absent.

func datasourceOpts(f *protogen.File) *protormpbv1.DatasourceOptions {
	if !proto.HasExtension(f.Desc.Options(), protormpbv1.E_Datasource) {
		return &protormpbv1.DatasourceOptions{}
	}
	return proto.GetExtension(f.Desc.Options(), protormpbv1.E_Datasource).(*protormpbv1.DatasourceOptions)
}

func tableOpts(msg *protogen.Message) *protormpbv1.TableOptions {
	if !proto.HasExtension(msg.Desc.Options(), protormpbv1.E_Table) {
		return &protormpbv1.TableOptions{}
	}
	return proto.GetExtension(msg.Desc.Options(), protormpbv1.E_Table).(*protormpbv1.TableOptions)
}

func colOpts(f *protogen.Field) *protormpbv1.ColOptions {
	if !proto.HasExtension(f.Desc.Options(), protormpbv1.E_Col) {
		return &protormpbv1.ColOptions{}
	}
	return proto.GetExtension(f.Desc.Options(), protormpbv1.E_Col).(*protormpbv1.ColOptions)
}

func resourceOf(msg *protogen.Message) *annotations.ResourceDescriptor {
	if !proto.HasExtension(msg.Desc.Options(), annotations.E_Resource) {
		return nil
	}
	return proto.GetExtension(msg.Desc.Options(), annotations.E_Resource).(*annotations.ResourceDescriptor)
}

func resourceRef(f *protogen.Field) *annotations.ResourceReference {
	if !proto.HasExtension(f.Desc.Options(), annotations.E_ResourceReference) {
		return nil
	}
	return proto.GetExtension(f.Desc.Options(), annotations.E_ResourceReference).(*annotations.ResourceReference)
}

func fieldBehaviors(f *protogen.Field) []annotations.FieldBehavior {
	if !proto.HasExtension(f.Desc.Options(), annotations.E_FieldBehavior) {
		return nil
	}
	return proto.GetExtension(f.Desc.Options(), annotations.E_FieldBehavior).([]annotations.FieldBehavior)
}

// cleanComment normalizes a proto leading comment for embedding in generated output.
// It strips // markers from every line and collapses the result to a single string.
// Returns "" when the comment is absent or blank.
func cleanComment(c protogen.Comments) string {
	s := strings.TrimSpace(string(c))
	if s == "" {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

// modelNameFromType extracts the singular resource name from a resource type string.
// "bookstore.v1/Author" → "Author".
func modelNameFromType(resourceType string) string {
	parts := strings.SplitN(resourceType, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return resourceType
}

// sourceFileBase returns the proto file base name without directories or
// extension: "bookstore/v1/bookstore.proto" → "bookstore".
func sourceFileBase(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSuffix(path, ".proto")
}

// protoDirNoVersion returns the proto file's directory with a trailing API
// version segment dropped, so the generated tree mirrors the proto tree without
// the version noise: "store/apps/productivity/calendar/v1/event.proto" →
// "store/apps/productivity/calendar". A file at the module root returns "".
func protoDirNoVersion(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return ""
	}
	dir := path[:i]
	if last := strings.LastIndexByte(dir, '/'); last >= 0 {
		if isVersionSegment(dir[last+1:]) {
			return dir[:last]
		}
	} else if isVersionSegment(dir) {
		return ""
	}
	return dir
}

// isVersionSegment reports whether seg is an API version directory like "v1",
// "v2", "v1alpha1", or "v1beta1": a 'v' followed by a digit.
func isVersionSegment(seg string) bool {
	return len(seg) >= 2 && seg[0] == 'v' && seg[1] >= '0' && seg[1] <= '9'
}
