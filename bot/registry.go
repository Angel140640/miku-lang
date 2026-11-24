package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"miku/lang"
)

// State é o que fica salvo em ~/.miku/modules.json.
type State struct {
	Installed []string          `json:"installed"` // módulos embutidos ligados
	Custom    map[string]string `json:"custom"`    // módulos .mk de terceiros: nome -> arquivo
}

// Home é a pasta de dados da Miku (~/.miku ou $MIKU_HOME).
func Home() string {
	if h := os.Getenv("MIKU_HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".miku")
	}
	return ".miku"
}

func statePath() string { return filepath.Join(Home(), "modules.json") }

func LoadState() State {
	var s State
	if raw, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if s.Custom == nil {
		s.Custom = map[string]string{}
	}
	return s
}

func (s State) save() error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	sort.Strings(s.Installed)
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), raw, 0o644)
}

// InstalledSet devolve os módulos embutidos que estão ligados.
func InstalledSet() map[string]bool {
	set := map[string]bool{}
	for _, n := range LoadState().Installed {
		if IsBuiltinModule(n) {
			set[n] = true
		}
	}
	return set
}

// CustomPaths devolve os módulos .mk de terceiros, em ordem alfabética.
func CustomPaths() []string {
	s := LoadState()
	names := make([]string, 0, len(s.Custom))
	for n := range s.Custom {
		names = append(names, n)
	}
	sort.Strings(names)
	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, s.Custom[n])
	}
	return paths
}

type ModuleInfo struct {
	Name      string
	Desc      string
	Installed bool
	Custom    bool
}

func List() []ModuleInfo {
	s := LoadState()
	on := map[string]bool{}
	for _, n := range s.Installed {
		on[n] = true
	}
	var out []ModuleInfo
	for _, m := range Modules {
		out = append(out, ModuleInfo{Name: m.Name, Desc: m.Desc, Installed: on[m.Name]})
	}
	names := make([]string, 0, len(s.Custom))
	for n := range s.Custom {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, ModuleInfo{Name: n, Desc: "módulo externo (" + s.Custom[n] + ")", Installed: true, Custom: true})
	}
	return out
}

var moduleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Install liga um módulo embutido ou instala um módulo .mk (arquivo local ou URL).
func Install(target string) (string, error) {
	s := LoadState()
	if IsBuiltinModule(target) {
		for _, n := range s.Installed {
			if n == target {
				return "o módulo '" + target + "' já estava instalado", nil
			}
		}
		s.Installed = append(s.Installed, target)
		if err := s.save(); err != nil {
			return "", err
		}
		return "módulo '" + target + "' instalado", nil
	}

	isURL := strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
	isFile := strings.HasSuffix(target, ".mk") || strings.ContainsAny(target, `/\`)
	if !isURL && !isFile {
		return "", fmt.Errorf("o módulo '%s' não existe. Veja os disponíveis com: miku list", target)
	}

	var src []byte
	var base string
	if isURL {
		u, err := url.Parse(target)
		if err != nil {
			return "", err
		}
		base = filepath.Base(u.Path)
		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Get(target)
		if err != nil {
			return "", fmt.Errorf("não consegui baixar o módulo: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("não consegui baixar o módulo: %s", resp.Status)
		}
		if src, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20+1)); err != nil {
			return "", err
		}
		if len(src) > 1<<20 {
			return "", errors.New("o módulo passa de 1 MB, recusei")
		}
	} else {
		var err error
		if src, err = os.ReadFile(target); err != nil {
			return "", fmt.Errorf("não consegui ler %s: %v", target, err)
		}
		base = filepath.Base(target)
	}

	name := strings.ToLower(strings.TrimSuffix(base, ".mk"))
	if !moduleNameRe.MatchString(name) {
		return "", fmt.Errorf("nome de módulo '%s' inválido: use minúsculas, números, - ou _ (comece com letra)", name)
	}
	if IsBuiltinModule(name) {
		return "", fmt.Errorf("'%s' é nome de um módulo embutido; renomeie o arquivo", name)
	}
	if _, err := lang.Parse(string(src)); err != nil {
		return "", fmt.Errorf("o módulo tem erro de sintaxe: %v", err)
	}
	dir := filepath.Join(Home(), "mods")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name+".mk")
	if err := os.WriteFile(dest, src, 0o644); err != nil {
		return "", err
	}
	s.Custom[name] = dest
	if err := s.save(); err != nil {
		return "", err
	}
	msg := "módulo '" + name + "' instalado"
	if isURL {
		msg += " (ele roda com as mesmas permissões do seu bot: instale só de fontes confiáveis)"
	}
	return msg, nil
}

func Uninstall(name string) (string, error) {
	s := LoadState()
	if path, ok := s.Custom[name]; ok {
		delete(s.Custom, name)
		_ = os.Remove(path)
		if err := s.save(); err != nil {
			return "", err
		}
		return "módulo '" + name + "' removido", nil
	}
	for i, n := range s.Installed {
		if n == name {
			s.Installed = append(s.Installed[:i], s.Installed[i+1:]...)
			if err := s.save(); err != nil {
				return "", err
			}
			return "módulo '" + name + "' removido", nil
		}
	}
	return "", fmt.Errorf("o módulo '%s' não está instalado", name)
}
