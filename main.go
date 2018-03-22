package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-ini/ini"
	"github.com/pkg/errors"
)

type Repo struct {
	Name   string
	Secret string `ini:"secret"`
	Branch string `ini:"branch"`
}

type GithubPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type BuildJob struct {
	Name   string
	Url    string
	Branch string
}

var worker chan BuildJob

func queueWork(job BuildJob) {
	go (func() {
		worker <- job
	})()
}

func jobRunner() {
	worker = make(chan BuildJob)

	for {
		job := <-worker

		start := time.Now()
		log.Printf("┌running build job on %s|%s\n", job.Name, job.Branch)

		err := (func() error {
			// Set up working directory and prepare
			tmpDir := "/tmp/spectacle-" + strings.Replace(job.Name, "/", "-", -1)
			buildPath := tmpDir + "/src/github.com/" + job.Name
			if info, _ := os.Stat(tmpDir); info != nil {
				if err := os.RemoveAll(tmpDir); err != nil {
					log.Printf("├could not remove temporary files, %s", err.Error())
					return errors.Wrap(err, "remove failed")
				}
			}
			os.MkdirAll(buildPath, os.ModePerm)
			filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
				if err == nil {
					err = os.Chown(path, 1001, 1001)
				}
				return err
			})

			// Fetch code
			gitCmd := exec.Command("git", "clone", job.Url, buildPath)
			gitCmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{
					Uid: 1001,
					Gid: 1001,
				},
			}
			if err := gitCmd.Run(); err != nil {
				log.Printf("├failed to prepare for build, %s", err.Error())
				return errors.Wrap(err, "git command failed")
			}

			// Find and run build/service script
			if _, err := os.Stat(buildPath + "/spectacle.sh"); os.IsNotExist(err) {
				log.Println("├no spectacle.sh, aborting")
				return errors.Wrap(err, "missing spectacle.sh")
			}
			buildCmd := exec.Command("sh", "spectacle.sh")
			buildCmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{
					Uid: 1001,
					Gid: 1001,
				},
			}
			buildCmd.Dir = buildPath
			buildCmd.Env = []string{
				"HOME=/home/spectacle",
				"GOPATH=" + tmpDir,
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/bin",
			}
			if err := buildCmd.Run(); err != nil {
				log.Printf("├failed to complete, %s", err.Error())
				return errors.Wrap(err, "error when running spectacle.sh")
			}

			return nil
		})()

		status := "OK"
		if err != nil {
			status = "FAIL"
		}
		log.Printf("└[%s] in %.2fs\n", status, float64(time.Since(start))/float64(time.Second))
	}
}

type HookHandler struct {
	Repos []Repo
}

func (h HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "spectacle")

	start := time.Now()
	log.Printf("┌%s", r.URL.Path)
	defer func() {
		log.Printf("└done in %.2fms", float64(time.Since(start))/float64(time.Millisecond))
	}()

	if r.URL.Path != "/hook" {
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	} else if r.Method != "POST" {
		http.Error(w, "405 forbidden", http.StatusMethodNotAllowed)
		return
	} else if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	} else if len(r.Header.Get("X-Hub-Signature")) < 45 {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	}

	raw, _ := ioutil.ReadAll(r.Body)

	payload := GithubPayload{}
	err := json.Unmarshal(raw, &payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Find config
	repo, ok := (func() (*Repo, bool) {
		for _, repo := range h.Repos {
			if repo.Name == payload.Repository.FullName {
				return &repo, true
			}
		}
		return nil, false
	})()

	if !ok {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	}

	// Verify signature
	mac := hmac.New(sha1.New, []byte(repo.Secret))
	mac.Write(raw)
	sum := mac.Sum(nil)
	actual := make([]byte, 20)
	hex.Decode(actual, []byte(r.Header.Get("X-Hub-Signature")[5:]))
	if !hmac.Equal(sum, actual) {
		http.Error(w, "403 forbidden", http.StatusForbidden)
		return
	}

	// Handle event
	event := r.Header.Get("X-GitHub-Event")
	log.Printf("├incoming hook: %s|%s\n", repo.Name, event)
	switch event {
	case "watch":
		log.Println("├to be implemented\n")
	case "push":
		if !strings.HasSuffix(payload.Ref, repo.Branch) {
			log.Printf("├ignored ref \"%s\"\n", payload.Ref)
			break
		}

		log.Println("├queued build")
		queueWork(BuildJob{
			Name:   repo.Name,
			Url:    "https://github.com/" + repo.Name,
			Branch: repo.Branch,
		})
	default:
		log.Println("├unhandled")
	}

	w.WriteHeader(http.StatusAccepted)
}

func main() {
	handler := HookHandler{
		Repos: make([]Repo, 0, 10),
	}

	cfg, err := ini.Load("spectacle.ini")
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not read config"))
	}
	cfg.BlockMode = false

	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == "DEFAULT" {
			continue
		}

		repo := Repo{
			Name: name,
		}
		if err := section.MapTo(&repo); err != nil {
			log.Fatal(errors.Wrap(err, "failed to map repo config"))
		}
		handler.Repos = append(handler.Repos, repo)
	}

	names := []string{}
	for _, repo := range handler.Repos {
		names = append(names, repo.Name)
	}
	log.Println("registered repos:", strings.Join(names, ", "))

	go jobRunner()

	server := &http.Server{
		Addr:           ":8283",
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Println("going up...")
	err = server.ListenAndServe()
	if err != nil {
		log.Fatal("could not start server,", err)
	}
}
