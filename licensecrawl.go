package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"k8s.io/klog"

	"github.com/sonatype-nexus-community/nancy/parse"
	"github.com/sonatype-nexus-community/nancy/types"

	"github.com/go-enry/go-license-detector/v4/licensedb"
	"github.com/go-enry/go-license-detector/v4/licensedb/api"
	"github.com/go-enry/go-license-detector/v4/licensedb/filer"
	"github.com/go-git/go-git/v5"
)

var projectname = "/Users/bo/prg/ndbo"
var modulesHome = "/Users/bo/prg/go/pkg/mod/"

type modDetails struct {
	useCount int
	hasGoMod bool
}

// modVersion as [Version]Count
type modVersion map[string]modDetails

// depX as [Module Path]Version
var dep3 map[string]modVersion
var dep4 map[string]modVersion

var domains map[string]bool
var missing map[string]bool

func moduleExists(dep *map[string]modVersion, r *modfile.Require, replaces *[]*modfile.Replace) bool {
	exists := false

	version := r.Mod.Version
	path := r.Mod.Path
	for _, r := range *replaces {
		if path == r.Old.Path {
			version = r.New.Version
		}
	}

	if m, ok := (*dep)[r.Mod.Path]; ok {
		if _, ok := m[version]; ok {
			exists = true
		}
	}

	return exists
}

func incUseCount(dep *map[string]modVersion, path string, version string) {
	details := (*dep)[path][version]
	details.useCount++
	(*dep)[path][version] = details
}

func setHasGoMod(dep *map[string]modVersion, path string, version string) {
	details := (*dep)[path][version]
	details.hasGoMod = true
	(*dep)[path][version] = details
}

func addModuleFromVersion(dep *map[string]modVersion, mv *module.Version, replaces *[]*modfile.Replace) bool {

	version := mv.Version
	path := mv.Path

	// as a case with old.version != "" is not handled we exit
	if replaces != nil && len(*replaces) > 0 {
		for _, r := range *replaces {
			if r.Old.Version != "" {
				fmt.Printf("old: %s, new: %s, oldp: %s, newp: %s, oldv: %s\n",
					r.Old, r.New, r.Old.Path, r.New.Path, r.Old.Version)
				os.Exit(0)
			}
		}

		for _, r := range *replaces {
			if path == r.Old.Path {
				version = r.New.Version
			}
		}
	}

	// does module already exist in 3rd party module list?
	if m, ok := (*dep)[mv.Path]; !ok {
		m = make(modVersion)
		m[version] = modDetails{useCount: 0, hasGoMod: false}
		(*dep)[mv.Path] = m
	} else {
		// it does exist, does it's version exist?
		if _, ok := m[version]; !ok {
			m[version] = modDetails{useCount: 0, hasGoMod: false}
		}
	}

	ds := strings.Split(mv.Path, "/")
	domains[ds[0]] = true

	ret := false
	if (*dep)[mv.Path][version].useCount > 0 {
		ret = true
	}
	incUseCount(dep, mv.Path, version)
	return ret
}

func addModule(dep *map[string]modVersion, r *modfile.Require, replaces *[]*modfile.Replace) bool {
	return addModuleFromVersion(dep, &r.Mod, replaces)
}

// getRequiresFromMod takes a project path
// return the modules in the require section of its go.mod file
func getRequiresFromMod(projectPath string) ([]*modfile.Require, []*modfile.Replace) {
	path := projectPath
	fp := filepath.Join(path, "go.mod")
	d, err := ioutil.ReadFile(fp)
	if err != nil {
		//fmt.Printf("%s does not exist\n", fp)
		//fmt.Println()
		return nil, nil
	}
	var f *modfile.File
	f, err = modfile.Parse(fp, d, nil)
	if err != nil {
		os.Exit(1)
	}

	return f.Require, f.Replace
}
func projectListFromSumFile(sumfile string) types.ProjectList {
	file, err := os.Open(sumfile)
	if err != nil {
		klog.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	projList, err := parse.GoList(scanner)

	if err != nil {
		os.Exit(1)
	}

	return projList
}

var replaceUrls = map[string]string{
	"golang.org/x":        "github.com/golang",
	"honnef.co/go":        "github.com/dominikh",
	"cloud.google.com/go": "github.com/googleapis/google-cloud-go",
	"k8s.io":              "github.com/kubernetes",
	"modernc.org":         "gitlab.com/cznic",
	"sigs.k8s.io":         "github.com/kubernetes-sigs",
	"go.mongodb.org":      "github.com/mongodb",
	"mvdan.cc":            "github.com/mvdan",
}

func main() {

	// storage for 4th party dependencies
	dep4 = make(map[string]modVersion)
	domains = make(map[string]bool)

	projList := projectListFromSumFile(projectname + "/go.sum")

	for _, p := range projList.Projects {

		v := p.Version

		// version may look like "v1.19.0/go.mod"
		vs := strings.Split(v, "/")

		if len(vs) > 1 && vs[1] != "go.mod" {
			fmt.Println("Unknow format for ", p.Name+"@"+v)
			os.Exit(1)
		} else {
			v = vs[0]
		}

		addModuleFromVersion(&dep4, &module.Version{Path: p.Name, Version: v}, nil)
	}

	for modName, dep := range dep4 {
		for modVersion := range dep {

			path, err := module.EscapePath(modName)
			if err != nil {
				fmt.Println("Issues with escape path of ", modName)
				os.Exit(1)
			} else {
				//if path != p.Name {
				//	fmt.Println(p.Name, " => ", path)
				//}
			}

			fp := filepath.Join(modulesHome, path+"@"+modVersion)

			local := "[ ]"

			var licenses map[string]api.Match
			errorReading := ""

			// if not cached locally then use url
			if _, err := os.Stat(fp); os.IsNotExist(err) {
				local = "[-]"
				url := "https://" + modName

				// replace urls with known urls
				// TODO extract meta tags

				// <meta name=go-import content="go.my.org/myproj git https://github.com/my/myproj.git">
				// <meta name="go-source"
				//   content="go.my.org/myproj
				//			https://github.com/my/myproj
				//			https://github.com/my/myproj/tree/master{/dir}
				//			https://github.com/github.com/my/myproj/blob/master{/dir}/{file}#L{line}">

				for rep, repWith := range replaceUrls {
					if strings.HasPrefix(modName, rep) {
						s := strings.Replace(modName, rep, repWith, 1)
						url = "https://" + s
						//fmt.Println(modName + " => " + url)
					}
				}

				dir := filepath.Join("repos", path+"@"+modVersion)

				repo, err := git.PlainClone(dir, false, &git.CloneOptions{Progress: os.Stdout, URL: url})
				if err != nil {
					if err == git.ErrRepositoryAlreadyExists {
						repo, err = git.PlainOpen(dir)
						if err != nil {
							//fmt.Printf("%s %s  ", local, url)
							errorReading = fmt.Sprintf("Error with cloning from git url %s: %s", url, err)
						}
					} else {
						//fmt.Printf("%s %s  ", local, url)
						errorReading = fmt.Sprintf("Error with cloning from git url %s: %s", url, err)
					}
				}

				if errorReading == "" {
					flr, err := filer.FromGit(repo, "")

					if err != nil {
						fmt.Printf("%s %s  ", local, url)
						fmt.Println("Error with filer from git url: ", err)
						os.Exit(1)
					}
					licenses, err = licensedb.Detect(flr)
				}

			} else {

				flr, err := filer.FromDirectory(fp)
				if err != nil {
					fmt.Printf("%s %s  ", local, fp)
					fmt.Println("Error with filer from dir: ", err)
					os.Exit(1)
				}
				licenses, err = licensedb.Detect(flr)
			}

			highestConfidence := float64(0.0)
			whichLicense := errorReading
			if errorReading == "" {
				for licenseName, license := range licenses {
					if float64(license.Confidence) > highestConfidence {
						whichLicense = licenseName
						highestConfidence = float64(license.Confidence)
					}
				}
			}
			fmt.Printf("%s\t%s\t%s\t%f\t%s\n", local, modName, modVersion, highestConfidence, whichLicense)

			/*
				bytes, err := json.MarshalIndent(licenses, "", "\t")
				if err != nil {
					fmt.Printf("could not encode result to JSON: %v\n", err)
				}
			*/
		}
	}

}

// just trying to test module module and see how to understand dependencies manually
func main2() {

	dep3 = make(map[string]modVersion)
	dep4 = make(map[string]modVersion)

	domains = make(map[string]bool)
	missing = make(map[string]bool)

	// get all dependencies from this projects go.mod file
	rs3, rp3 := getRequiresFromMod(projectname)
	for _, r := range rs3 {
		addModule(&dep3, r, &rp3)
	}

	//	for _, r := range rp3 {
	//		fmt.Printf("old: %s, new: %s, oldp: %s, newp: %s, oldv: %s\n", r.Old, r.New, r.Old.Path, r.New.Path, r.Old.Version)
	//	}

	//	os.Exit(0)

	// check all 4th, 5th ... party dependencies (3rd party's 3rd party)
	anyMissing := true
	depends := &dep3

	for anyMissing {
		anyMissing = false
		for m, ver := range *depends {
			for v := range ver {
				//fmt.Printf("%s %s %d\n", m, v, len(*depends))

				path, err := module.EscapePath(m)
				if err != nil {
					fmt.Println("Issues with escape path of ", m)
					os.Exit(1)
				} else {
					//if path != m {
					//	fmt.Println(m, " => ", path)
					//}

				}

				fp := filepath.Join(modulesHome, path+"@"+v)

				rs4, rpl4 := getRequiresFromMod(fp)
				if rs4 == nil {
					missing[m+" "+v] = true
					continue
				}
				setHasGoMod(depends, m, v)

				for _, r4 := range rs4 {
					if !moduleExists(&dep4, r4, &rpl4) {
						fmt.Printf("Adding %s %s\n", r4.Mod.Path, r4.Mod.Version)
						addModule(&dep4, r4, &rpl4)
						anyMissing = true
					}
				}
			}
			//fmt.Println("len ", len(*depends))
			//fmt.Println()
		}
		depends = &dep4
	}

	// print modules

	ms := make([]string, 0, len(dep4))
	for m := range dep4 {
		ms = append(ms, m)
	}
	sort.Strings(ms)

	count := 0
	countMissing := 0

	for _, m := range ms {
		//fmt.Println(m)
		ver := dep4[m]
		for v, cnt := range ver {
			fmt.Println(m, v, cnt)
			count++
			if !cnt.hasGoMod {
				countMissing++
			}
		}
	}

	fmt.Println("---")
	fmt.Println("#modules: ", count)
	fmt.Println("#missing: ", countMissing)
	fmt.Println()

	// print modules with missing go.mod

	count = 0
	miss := make([]string, 0, len(missing))
	fmt.Println("Missing go.mod")
	for mis := range missing {
		miss = append(miss, mis)
	}
	sort.Strings(miss)
	for _, mis := range miss {
		fmt.Println(mis)
		count++
	}
	fmt.Println("---")
	fmt.Println("#modules: ", count)
}
