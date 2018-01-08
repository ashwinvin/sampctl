package rook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fatih/color"
	"gopkg.in/AlecAivazis/survey.v1"

	"github.com/Southclaws/sampctl/print"
	"github.com/Southclaws/sampctl/types"
	"github.com/Southclaws/sampctl/util"
	"github.com/Southclaws/sampctl/versioning"
)

// Init prompts the user to initialise a package
func Init(dir string) (err error) {
	var (
		pwnFiles []string
		incFiles []string
		dirName  = filepath.Base(dir)
	)

	if !util.Exists(dir) {
		return errors.New("directory does not exist")
	}

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) (innerErr error) {
		if info.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		rel, innerErr := filepath.Rel(dir, path)
		if innerErr != nil {
			return
		}

		if ext == ".pwn" {
			pwnFiles = append(pwnFiles, rel)
		} else if ext == ".inc" {
			incFiles = append(incFiles, rel)
		}

		return
	})
	if err != nil {
		return
	}

	color.Green("Found %d pwn files and %d inc files.", len(pwnFiles), len(incFiles))

	var questions = []*survey.Question{
		{
			Name: "Format",
			Prompt: &survey.Select{
				Message: "Preferred package format",
				Options: []string{"json", "yaml"},
			},
			Validate: survey.Required,
		},
		{
			Name: "User",
			Prompt: &survey.Input{
				Message: "Your Name - If you plan to release, must be your GitHub username.",
			},
			Validate: survey.Required,
		},
		{
			Name: "Repo",
			Prompt: &survey.Input{
				Message: "Package Name - If you plan to release, must be the GitHub project name.",
				Default: dirName,
			},
			Validate: survey.Required,
		},
		{
			Name:   "GitIgnore",
			Prompt: &survey.Confirm{Message: "Add a .gitignore file?", Default: true},
		},
		{
			Name:   "Readme",
			Prompt: &survey.Confirm{Message: "Add a README.md file?", Default: true},
		},
		{
			Name: "Editor",
			Prompt: &survey.Select{
				Message: "Select your text editor",
				Options: []string{"none", "vscode"},
			},
			Validate: survey.Required,
		},
		{
			Name:   "StdLib",
			Prompt: &survey.Confirm{Message: "Add standard library dependency?", Default: true},
		},
	}

	if len(pwnFiles) > 0 {
		questions = append(questions, &survey.Question{
			Name: "Entry",
			Prompt: &survey.Select{
				Message: "Choose an entry point - this is the file that is passed to the compiler.",
				Options: pwnFiles,
			},
			Validate: survey.Required,
		})
	} else {
		if len(incFiles) > 0 {
			questions = append(questions, &survey.Question{
				Name: "EntryGenerate",
				Prompt: &survey.MultiSelect{
					Message: "No .pwn found but .inc found - create .pwn file that includes .inc?",
					Options: incFiles,
				},
				Validate: survey.Required,
			})
		} else {
			questions = append(questions, &survey.Question{
				Name: "Entry",
				Prompt: &survey.Input{
					Message: "No .pwn or .inc files - enter name for new script",
					Default: "test.pwn",
				},
			})
		}
	}

	answers := struct {
		Format        string
		User          string
		Repo          string
		GitIgnore     bool
		Readme        bool
		Editor        string
		StdLib        bool
		EntryGenerate []string
		Entry         string
	}{}

	err = survey.Ask(questions, &answers)
	if err != nil {
		return
	}

	pkg := types.Package{
		Parent: true,
		Local:  dir,
		Format: answers.Format,
		DependencyMeta: versioning.DependencyMeta{
			User: answers.User,
			Repo: answers.Repo,
		},
	}

	if answers.Entry != "" {
		ext := filepath.Ext(answers.Entry)
		nameOnly := strings.TrimSuffix(answers.Entry, ext)
		pkg.Entry = nameOnly + ".pwn"
		pkg.Output = nameOnly + ".amx"

		if ext != "" && ext != ".pwn" {
			print.Warn("Entry point is not a .pwn file - it's advised to use a .pwn file as the compiled script.")
			print.Warn("If you are writing a library and not a gamemode or filterscript,")
			print.Warn("it's good to make a separate .pwn file that #includes the .inc file of your library.")
		}
	} else {
		if len(answers.EntryGenerate) > 0 {
			buf := bytes.Buffer{}

			buf.WriteString(`// generated by "sampctl package generate"`)
			buf.WriteString("\n\n")
			for _, inc := range answers.EntryGenerate {
				buf.WriteString(fmt.Sprintf(`#include "%s"%s`, filepath.Base(inc), "\n"))
			}
			buf.WriteString("\nmain() {\n")
			buf.WriteString(`	// write tests for libraries here and run "sampctl package run"`)
			buf.WriteString("\n}\n")
			err = ioutil.WriteFile(filepath.Join(dir, "test.pwn"), buf.Bytes(), 0755)
			if err != nil {
				color.Red("failed to write generated tests.pwn file: %v", err)
			}
		}
		pkg.Entry = "test.pwn"
		pkg.Output = "test.amx"
	}

	wg := sync.WaitGroup{}

	if answers.GitIgnore {
		wg.Add(1)
		go func() {
			getTemplateFile(dir, ".gitignore")
			wg.Done()
		}()
	}

	if answers.Readme {
		wg.Add(1)
		go func() {
			getTemplateFile(dir, "README.md")
			wg.Done()
		}()
	}

	switch answers.Editor {
	case "vscode":
		wg.Add(1)
		go func() {
			getTemplateFile(dir, ".vscode/tasks.json")
			wg.Done()
		}()
	}

	if answers.StdLib {
		pkg.Dependencies = append(pkg.Dependencies, versioning.DependencyString("sampctl/samp-stdlib"))
	}

	err = pkg.WriteDefinition()
	if err != nil {
		print.Erro(err)
	}

	wg.Wait()

	err = EnsureDependencies(&pkg)

	return
}

func getTemplateFile(dir, filename string) (err error) {
	resp, err := http.Get("https://raw.githubusercontent.com/Southclaws/pawn-package-template/master/" + filename)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	outputFile := filepath.Join(dir, filename)

	if util.Exists(outputFile) {
		outputFile = "init-" + outputFile
	}

	err = os.MkdirAll(filepath.Dir(outputFile), 0755)
	if err != nil {
		return
	}

	file, err := os.Create(outputFile)
	if err != nil {
		return
	}
	defer func() {
		err = file.Close()
		if err != nil {
			print.Erro(err)
		}
	}()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return
	}

	return
}
