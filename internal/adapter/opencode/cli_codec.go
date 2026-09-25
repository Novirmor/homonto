package opencode

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter/jsoncodec"
	"github.com/tidwall/gjson"
)

type cliCodec struct{ jsoncodec.Codec }

func (cliCodec) checkParents(doc []byte, path string) error {
	escaped := false
	for i, ch := range path {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch != '.' {
			continue
		}
		parent := gjson.GetBytes(doc, path[:i])
		if parent.Exists() && !parent.IsObject() {
			return fmt.Errorf("opencode cli.json: %s is not an object; refusing to replace an unmanaged parent of %s", path[:i], strings.ReplaceAll(path, `\.`, "."))
		}
	}
	return nil
}

func (c cliCodec) Get(doc []byte, path string) (string, bool, error) {
	if err := c.checkParents(doc, path); err != nil {
		return "", false, err
	}
	return c.Codec.Get(doc, path)
}

func (c cliCodec) Set(doc []byte, path, value string) ([]byte, error) {
	if err := c.checkParents(doc, path); err != nil {
		return nil, err
	}
	return c.Codec.Set(doc, path, value)
}

func (c cliCodec) Delete(doc []byte, path string) ([]byte, error) {
	if !gjson.GetBytes(doc, path).Exists() {
		return doc, nil
	}
	return c.Codec.Delete(doc, path)
}
