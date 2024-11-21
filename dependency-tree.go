package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

var (
	gopath      = ""
	maxDepth    = flag.Int("max-depth", -1, "Maximum recursion level to scan, -1 for no limit, otherwise must be an integer greater than 0, ignored if -find specified. Defaults to -1.")
	modulePath  = flag.String("module-path", ".", "Path to module to scan, can be relative or absolute. Defaults to current working directory.")
	versionFlag = flag.Bool("version", false, "Print out go-tree version.")
)

func main() {
	flag.Parse()

	if *versionFlag {
		fmt.Println("v1.2.1")
		os.Exit(0)
	}

	if *maxDepth == 0 || *maxDepth < -1 {
		fmt.Println("Invalid value supplied to for maxDepth, must either be -1 or an integer grater than 0")
	}

	cwd := strings.TrimSpace(*modulePath)

	if cwd == "." {
		dir, err := os.Getwd()
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		cwd = dir

	} else if !path.IsAbs(cwd) && strings.HasPrefix(cwd, "~") {
		usr, err := user.Current()
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		cwd = filepath.Join(usr.HomeDir, strings.TrimPrefix(cwd, "~"))
	} else if !path.IsAbs(cwd) {
		dir, err := os.Getwd()
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		cwd = filepath.Join(dir, cwd)
	}

	gopath = os.Getenv("GOPATH")

	modFile := filepath.Join(cwd, "go.mod")
	if _, err := os.Stat(modFile); os.IsNotExist(err) {
		println("ERROR: go.mod is not present in this directory, please only run this tool in the root of your go project or specify a path to the root directory of a go project")
		os.Exit(1)
	}

	mod := module{
		packages: make(map[string]map[int]struct{}),
		indexes:  make(map[string]int),
		unknown:  make(map[string]struct{}),
		cache:    make(map[string]struct{}),
	}
	mod.List(cwd, *maxDepth)

	mod.Flush(os.Stdout)

	os.Exit(0)
}

type module struct {
	packages map[string]map[int]struct{}
	indexes  map[string]int
	unknown  map[string]struct{}
	cache    map[string]struct{}
}

func (m *module) List(cwd string, depth int) {
	modName := getModuleName(cwd)
	m.getModuleList(modName, "", depth)
}

func (m *module) Flush(writer io.Writer) {
	packages := make(map[string][]int)
	for pkg, indexes := range m.packages {
		if len(indexes) == 0 {
			fmt.Fprintln(os.Stderr, "No dependencies found for package: ", pkg)
		}

		for index := range indexes {
			packages[pkg] = append(packages[pkg], index)
		}
		sort.Slice(packages[pkg], func(i, j int) bool {
			return packages[pkg][i] < packages[pkg][j]
		})
	}

	indexes := make([]string, len(m.indexes))
	for pkg, index := range m.indexes {
		indexes[index] = pkg
	}

	unknown := make([]string, 0)
	for u := range m.unknown {
		unknown = append(unknown, u)
	}

	bytes, err := json.MarshalIndent(struct {
		Packages map[string][]int `json:"packages"`
		Indexes  []string         `json:"indexes"`
		Unknown  []string         `json:"unknown"`
	}{
		Packages: packages,
		Indexes:  indexes,
		Unknown:  unknown,
	}, "", "    ")
	if err != nil {
		fmt.Println("Error marshalling json: ", err)
		os.Exit(1)
	}
	fmt.Fprintln(writer, string(bytes))
}

func (m *module) getModuleList(modPath, indent string, depth int) {
	if _, ok := m.cache[modPath]; ok {
		return
	}
	m.cache[modPath] = struct{}{}

	if depth == 0 {
		return
	}

	rawPath, modFound := constructFilePath(escapeCapitalsInModuleName(modPath))
	if !modFound {
		m.unknown[modPath] = struct{}{}
		return
	}

	modFilePath := filepath.Join(rawPath, "go.mod")
	fileBytes, err := os.ReadFile(modFilePath)
	if err != nil {
		return
	}

	file, err := modfile.Parse(modFilePath, fileBytes, nil)
	if err != nil {
		return
	}

	for _, require := range file.Require {
		line := require.Mod.Path + " " + require.Mod.Version

		index, ok := m.indexes[line]
		if !ok {
			index = len(m.indexes)
			m.indexes[line] = index
		}

		if _, ok := m.packages[modPath]; !ok {
			m.packages[modPath] = make(map[int]struct{})
		}
		m.packages[modPath][index] = struct{}{}

		m.getModuleList(line, indent+"  ", depth-1)
	}
}

func getNameAndVersion(module string) (string, string) {
	if strings.Contains(module, "@") {
		s := strings.Split(module, "@")
		return s[0], s[1]
	}

	s := strings.Split(module, " ")
	if len(s) == 1 {
		return s[0], ""
	}

	return s[0], getSemVer(s[1])
}

func constructFilePath(dep string) (string, bool) {
	module, version := getNameAndVersion(dep)

	srcPath := filepath.Join(gopath, "src", module)
	if _, err := os.Stat(srcPath); err == nil || !os.IsNotExist(err) {
		return srcPath, true
	}

	pkgPath := filepath.Join(gopath, "pkg", "mod", module+"@"+getSemVer(version))
	if _, err := os.Stat(pkgPath); err == nil || !os.IsNotExist(err) {
		return pkgPath, true
	}

	fullVersionPkgPath := filepath.Join(gopath, "pkg", "mod", module+"@"+version)
	if _, err := os.Stat(fullVersionPkgPath); err == nil || !os.IsNotExist(err) {
		return fullVersionPkgPath, true
	}

	return "", false
}

var re = regexp.MustCompile(`v\d+\.\d+\.\d+`)

func getSemVer(version string) string {
	match := re.FindStringSubmatch(version)
	if len(match) == 0 {
		return ""
	} else if len(match) == 1 {
		return match[0]
	}
	return match[1]
}

func getModuleName(cwd string) string {
	modFilePath := path.Join(cwd, "go.mod")
	fileBytes, err := os.ReadFile(modFilePath)

	if err != nil {
		fmt.Println("Error reading go.mod: ", err)
		os.Exit(1)
	}

	f, err := modfile.Parse(modFilePath, fileBytes, nil)
	if err != nil {
		fmt.Println("Error parsing go.mod: ", err)
		os.Exit(1)
	}
	return f.Module.Mod.Path
}

func escapeCapitalsInModuleName(name string) string {
	letters := strings.Split(name, "")
	newName := ""
	for _, letter := range letters {
		if strings.ToLower(letter) != letter {
			newName += "!" + strings.ToLower(letter)
		} else {
			newName += letter
		}
	}
	return newName
}
