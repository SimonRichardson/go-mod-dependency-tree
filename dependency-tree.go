package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
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
		cache:   make(map[string]struct{}),
		writer:  new(bytes.Buffer),
		unknown: new(bytes.Buffer),
	}
	mod.List(cwd, *maxDepth)

	mod.Flush(os.Stdout)

	os.Exit(0)
}

type module struct {
	cache   map[string]struct{}
	writer  *bytes.Buffer
	unknown *bytes.Buffer
}

func (m *module) List(cwd string, depth int) {
	modName := getModuleName(cwd)
	m.getModuleList(modName, "", depth)
}

func (m *module) Flush(writer io.Writer) {
	fmt.Fprintln(writer, "--------------------")
	fmt.Fprintln(writer, "Direct dependencies:")
	fmt.Fprintln(writer, "--------------------")
	fmt.Fprintln(writer, m.writer.String())
	fmt.Fprintln(writer, "-----------------------------------------------------")
	fmt.Fprintln(writer, "Transient (not local / not compiled in) dependencies:")
	fmt.Fprintln(writer, "-----------------------------------------------------")
	fmt.Fprintln(writer, m.unknown.String())
}

func (m *module) getModuleList(modPath, indent string, depth int) {
	if depth == 0 {
		m.writeLine(indent, modPath)
		return
	}

	if _, ok := m.cache[modPath]; ok {
		return
	}
	m.cache[modPath] = struct{}{}

	rawPath, modFound := constructFilePath(escapeCapitalsInModuleName(modPath))
	if !modFound {
		m.writeUnknownLine(modPath)
		return
	}

	modFilePath := filepath.Join(rawPath, "go.mod")

	m.writeLine(indent, modPath)

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
		m.getModuleList(line, indent+"  ", depth-1)
	}
}

func (m *module) writeLine(indent, line string) {
	fmt.Fprintln(m.writer, strings.Split(line, " //")[0])
}

func (m *module) writeUnknownLine(line string) {
	fmt.Fprintln(m.unknown, strings.Split(line, " //")[0])
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

	lines := strings.Split(string(fileBytes), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			modAddress := strings.Split(line, "module ")[1]
			if strings.Contains(modAddress, "\"") {
				modAddress = modAddress[1 : len(modAddress)-1]
			}
			var modName string
			if strings.HasSuffix(cwd, modAddress) {
				modName = modAddress
			} else {
				modName = modAddress + strings.Split(cwd, modAddress)[1]
			}
			return modName
		}
	}

	fmt.Println("Invalid go.mod, not module name")
	os.Exit(1)
	return ""
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
