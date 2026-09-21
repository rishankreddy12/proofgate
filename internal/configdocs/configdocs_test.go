package configdocs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

// tags walks a struct type and collects every yaml tag name.
func tags(t reflect.Type, out map[string]bool) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
		tags(f.Type, out)
	}
}

func TestEveryConfigFieldIsDocumented(t *testing.T) {
	names := map[string]bool{}
	tags(reflect.TypeOf(config.Config{}), names)
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	require.NoError(t, err)
	doc := string(b)
	var missing []string
	for n := range names {
		if !strings.Contains(doc, "`"+n+"`") {
			missing = append(missing, n)
		}
	}
	require.Empty(t, missing, "add these keys to docs/configuration.md: %v", missing)
}
