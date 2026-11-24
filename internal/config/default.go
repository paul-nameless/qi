package config

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func SetDefaultProfile(path, name string) error {
	_, err := SetValue(path, "", "default_profile", name)
	return err
}

func SetValue(path, profile, key string, value any) (inProfile bool, err error) {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandHome(path)
	data, err := os.ReadFile(path)
	perm := fs.FileMode(0o600)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data = []byte("{}\n")
	case err != nil:
		return false, err
	default:
		if st, err := os.Stat(path); err == nil {
			perm = st.Mode().Perm()
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	out, inProfile, err := setIn(data, profile, key, encoded)
	if err != nil {
		return false, fmt.Errorf("%s: %w", ShortHome(path), err)
	}
	return inProfile, writeFileAtomic(path, out, perm)
}

func setIn(data []byte, profile, key string, encoded []byte) ([]byte, bool, error) {
	if profile != "" {
		out, found, err := replaceAt(data, []string{"profiles", profile, key}, encoded)
		if err != nil || found {
			return out, found, err
		}
	}
	out, err := setTopLevel(data, key, encoded)
	return out, false, err
}

func replaceAt(data []byte, path []string, encoded []byte) ([]byte, bool, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return data, false, nil
	}
	for dec.PeekKind() != '}' {
		k, err := dec.ReadToken()
		if err != nil {
			return nil, false, err
		}
		name := k.String()
		v, err := dec.ReadValue()
		if err != nil {
			return nil, false, err
		}
		if name != path[0] {
			continue
		}
		end := int(dec.InputOffset())
		start := end - len(v)
		sub := encoded
		if len(path) > 1 {
			var found bool
			if sub, found, err = replaceAt(data[start:end], path[1:], encoded); err != nil || !found {
				return data, false, err
			}
		}
		return append(append(append([]byte{}, data[:start]...), sub...), data[end:]...), true, nil
	}
	return data, false, nil
}

func setTopLevel(data []byte, key string, encoded []byte) ([]byte, error) {
	out, found, err := replaceAt(data, []string{key}, encoded)
	if err != nil || found {
		return out, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	tok, err := dec.ReadToken()
	if err != nil || tok.Kind() != '{' {
		return nil, errors.New("expected a JSON object")
	}
	open := dec.InputOffset()
	entry := fmt.Sprintf("%q: %s", key, encoded)
	rest := bytes.TrimLeft(data[open:], " \t\r\n")
	if bytes.HasPrefix(rest, []byte("}")) {
		return append(append(append([]byte{}, data[:open]...), entry...), rest...), nil
	}
	return append(append(append([]byte{}, data[:open]...), "\n  "+entry+","...), data[open:]...), nil
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
