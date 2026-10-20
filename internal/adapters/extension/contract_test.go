package extension

import (
	"regexp"
	"strings"
	"testing"

	_ "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// policyWords are field and enum value names that would let an extension
// answer a policy question.
var policyWords = regexp.MustCompile(`(?i)verdict|risk|allow|approv|deny|denied|decision|decide|policy|envelope|permit|reject|accept`)

// TestContractCarriesNoVerdict: no message reachable from the extension
// contract can carry a policy verdict. Every file of package
// oap.extension.v1 is checked: it imports nothing from OAP's own API, no
// field or enum value is named for a policy outcome, and no field is a
// free-form type (Struct, Value, Any, bytes) that could smuggle one.
func TestContractCarriesNoVerdict(t *testing.T) {
	files := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("oap.extension.v1", func(f protoreflect.FileDescriptor) bool {
		files++
		imports := f.Imports()
		for i := 0; i < imports.Len(); i++ {
			if p := imports.Get(i).Path(); strings.HasPrefix(p, "oap/") && !strings.HasPrefix(p, "oap/extension/") {
				t.Errorf("%s imports %s", f.Path(), p)
			}
		}
		var walk func(protoreflect.MessageDescriptors)
		walk = func(ms protoreflect.MessageDescriptors) {
			for i := 0; i < ms.Len(); i++ {
				m := ms.Get(i)
				if policyWords.MatchString(string(m.Name())) {
					t.Errorf("message %s is named for a policy outcome", m.FullName())
				}
				fields := m.Fields()
				for j := 0; j < fields.Len(); j++ {
					fd := fields.Get(j)
					if policyWords.MatchString(string(fd.Name())) {
						t.Errorf("field %s is named for a policy outcome", fd.FullName())
					}
					if fd.Kind() == protoreflect.BytesKind {
						t.Errorf("field %s is bytes, which could carry anything", fd.FullName())
					}
					if md := fd.Message(); md != nil && !fd.IsMap() {
						if pkg := string(md.ParentFile().Package()); pkg != "oap.extension.v1" && md.FullName() != "google.protobuf.Timestamp" && md.FullName() != "google.protobuf.Duration" {
							t.Errorf("field %s is a %s, which could carry anything", fd.FullName(), md.FullName())
						}
					}
					if ed := fd.Enum(); ed != nil && ed.ParentFile().Package() != "oap.extension.v1" {
						t.Errorf("field %s uses enum %s from outside the contract", fd.FullName(), ed.FullName())
					}
				}
				walk(m.Messages())
			}
		}
		walk(f.Messages())
		enums := f.Enums()
		for i := 0; i < enums.Len(); i++ {
			vals := enums.Get(i).Values()
			for j := 0; j < vals.Len(); j++ {
				if policyWords.MatchString(string(vals.Get(j).Name())) {
					t.Errorf("enum value %s is named for a policy outcome", vals.Get(j).FullName())
				}
			}
		}
		return true
	})
	if files < 2 {
		t.Fatalf("checked %d files of the contract; want every one", files)
	}
}
