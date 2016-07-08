package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	. "github.com/russross/codegrinder/types"
	"github.com/spf13/cobra"
)

const (
	defaultHost          = "dorking.cs.dixie.edu"
	perUserDotFile       = ".codegrinderrc"
	perProblemSetDotFile = ".grind"
)

var Config struct {
	Host      string `json:"host"`
	Cookie    string `json:"cookie"`
	apiReport bool
	apiDump   bool
}

type DotFileInfo struct {
	AssignmentID int64                   `json:"assignmentID"`
	Problems     map[string]*ProblemInfo `json:"problems"`
	Path         string                  `json:"-"`
}

type ProblemInfo struct {
	ID        int64           `json:"id"`
	Step      int64           `json:"step"`
	Whitelist map[string]bool `json:"whitelist"`
}

func main() {
	log.SetFlags(log.Ltime)

	cmdGrind := &cobra.Command{
		Use:   "grind",
		Short: "Command-line interface to CodeGrinder",
		Long: "A command-line tool to access CodeGrinder\n" +
			"by Russ Ross <russ@russross.com>",
	}
	cmdGrind.PersistentFlags().BoolP("api", "", false, "report all API requests")
	cmdGrind.PersistentFlags().BoolP("api-dump", "", false, "dump API request and response data")

	cmdVersion := &cobra.Command{
		Use:   "version",
		Short: "print the version number of grind",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("grind " + CurrentVersion.Version)
		},
	}
	cmdGrind.AddCommand(cmdVersion)

	cmdInit := &cobra.Command{
		Use:   "init",
		Short: "connect to codegrinder server",
		Run:   CommandInit,
	}
	cmdGrind.AddCommand(cmdInit)

	cmdList := &cobra.Command{
		Use:   "list",
		Short: "list all of your active assignments",
		Run:   CommandList,
	}
	cmdGrind.AddCommand(cmdList)

	cmdGet := &cobra.Command{
		Use:   "get",
		Short: "download an assignment to work on it locally",
		Long: "   Give either the numeric ID (given at the start of each listing)\n" +
			"   or the course/problem identifier (given in parentheses).\n\n" +
			"   Use \"grind list\" to see a list of assignments available to you.\n\n" +
			"   By default, the assignment will be stored in a directory matching the\n" +
			"   course/problem name, but you can override this by supplying the directory\n" +
			"   name as an additional argument.\n\n" +
			"   Example: grind get CS-1400/cs1400-loops\n\n" +
			"   Note: you must load an assignment through Canvas before you can access it.",
		Run: CommandGet,
	}
	cmdGrind.AddCommand(cmdGet)

	cmdSave := &cobra.Command{
		Use:   "save",
		Short: "save your work to the server without additional action",
		Run:   CommandSave,
	}
	cmdGrind.AddCommand(cmdSave)

	cmdGrade := &cobra.Command{
		Use:   "grade",
		Short: "save your work and submit it for grading",
		Run:   CommandGrade,
	}
	cmdGrind.AddCommand(cmdGrade)

	cmdCreate := &cobra.Command{
		Use:   "create",
		Short: "create a new problem (authors only)",
		Run:   CommandCreate,
	}
	cmdCreate.Flags().BoolP("update", "u", false, "update an existing problem")
	cmdGrind.AddCommand(cmdCreate)

	cmdGrind.Execute()
}

func CommandInit(cmd *cobra.Command, args []string) {
	mustLoadConfig(cmd)

	var loadedHost string
	loadedHost = defaultHost
	if Config.Host != "" {
		loadedHost = Config.Host
	}
	fmt.Println(
		`Please follow these steps:

1.  Use Canvas to load a CodeGrinder window
2.  Open a new tab in your browser and copy this URL into the address bar:

    https://` + loadedHost + `/v2/users/me/cookie

3.  The browser will display something of the form: ` + CookieName + `=...
4.  Copy that entire string to the clipboard and paste it below.

Paste here: `)

	var cookie string
	n, err := fmt.Scanln(&cookie)
	if err != nil {
		log.Fatalf("error encountered while reading the cookie you pasted: %v\n", err)
	}
	if n != 1 {
		log.Fatalf("failed to read the cookie you pasted; please try again\n")
	}
	if !strings.HasPrefix(cookie, CookieName+"=") {
		log.Fatalf("the cookie must start with %s=; perhaps you copied the wrong thing?\n", CookieName)
	}

	// set up config
	Config.Cookie = cookie
	Config.Host = loadedHost

	// see if they need an upgrade
	checkVersion()

	// try it out by fetching a user record
	user := new(User)
	mustGetObject("/users/me", nil, user)

	// save config for later use
	mustWriteConfig()

	log.Printf("cookie verified and saved: welcome %s", user.Name)
}

func mustGetObject(path string, params map[string]string, download interface{}) {
	doRequest(path, params, "GET", nil, download, false)
}

func getObject(path string, params map[string]string, download interface{}) bool {
	return doRequest(path, params, "GET", nil, download, true)
}

func mustPostObject(path string, params map[string]string, upload interface{}, download interface{}) {
	doRequest(path, params, "POST", upload, download, false)
}

func mustPutObject(path string, params map[string]string, upload interface{}, download interface{}) {
	doRequest(path, params, "PUT", upload, download, false)
}

func doRequest(path string, params map[string]string, method string, upload interface{}, download interface{}, notfoundokay bool) bool {
	if !strings.HasPrefix(path, "/") {
		log.Panicf("doRequest path must start with /")
	}
	if method != "GET" && method != "POST" && method != "PUT" && method != "DELETE" {
		log.Panicf("doRequest only recognizes GET, POST, PUT, and DELETE methods")
	}
	url := fmt.Sprintf("https://%s/v2%s", Config.Host, path)
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Fatalf("error creating http request: %v\n", err)
	}

	// add any parameters
	if params != nil && len(params) > 0 {
		values := req.URL.Query()
		for key, value := range params {
			values.Add(key, value)
		}
		req.URL.RawQuery = values.Encode()
	}

	if Config.apiReport {
		log.Printf("%s %s", method, req.URL)
	}

	// set the headers
	req.Header["Accept"] = []string{"application/json"}
	req.Header["Cookie"] = []string{Config.Cookie}

	// upload the payload if any
	if upload != nil && (method == "POST" || method == "PUT") {
		req.Header["Content-Type"] = []string{"application/json"}
		payload, err := json.MarshalIndent(upload, "", "    ")
		if err != nil {
			log.Fatalf("doRequest: JSON error encoding object to upload: %v", err)
		}
		req.Body = ioutil.NopCloser(bytes.NewReader(payload))

		if Config.apiDump {
			log.Printf("Request data: %s", payload)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("error connecting to %s: %v\n", Config.Host, err)
	}
	defer resp.Body.Close()
	if notfoundokay && resp.StatusCode == http.StatusNotFound {
		return false
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("unexpected status from %s: %s\n", url, resp.Status)
		io.Copy(os.Stderr, resp.Body)
		log.Fatalf("giving up")
	}

	// parse the result if any
	if download != nil {
		decoder := json.NewDecoder(resp.Body)
		if err := decoder.Decode(download); err != nil {
			log.Fatalf("failed to parse result object from server: %v\n", err)
		}

		if Config.apiDump {
			raw, err := json.MarshalIndent(download, "", "    ")
			if err != nil {
				log.Fatalf("doRequest: JSON error encoding downloaded object: %v", err)
			}
			log.Printf("Response data: %s", raw)
		}

		return true
	}
	return false
}

func mustLoadConfig(cmd *cobra.Command) {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		log.Fatalf("Unable to locate home directory, giving up\n")
	}
	configFile := filepath.Join(home, perUserDotFile)

	if raw, err := ioutil.ReadFile(configFile); err != nil {
		log.Fatalf("Unable to load config file; try running \"grind init\"\n")
	} else if err := json.Unmarshal(raw, &Config); err != nil {
		log.Printf("failed to parse %s: %v", configFile, err)
		log.Fatalf("you may wish to try deleting the file and running \"grind init\" again\n")
	}
	if cmd.Flag("api").Value.String() == "true" {
		Config.apiReport = true
	}
	if cmd.Flag("api-dump").Value.String() == "true" {
		Config.apiReport = true
		Config.apiDump = true
	}

	checkVersion()
}

func mustWriteConfig() {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		log.Fatalf("Unable to locate home directory, giving up\n")
	}
	configFile := filepath.Join(home, perUserDotFile)

	raw, err := json.MarshalIndent(&Config, "", "    ")
	if err != nil {
		log.Fatalf("JSON error encoding cookie file: %v", err)
	}
	raw = append(raw, '\n')

	if err = ioutil.WriteFile(configFile, raw, 0644); err != nil {
		log.Fatalf("error writing %s: %v", configFile, err)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func checkVersion() {
	server := new(Version)
	mustGetObject("/version", nil, server)
	grindCurrent := semver.MustParse(CurrentVersion.Version)
	grindRequired := semver.MustParse(server.GrindVersionRequired)
	if grindRequired.GT(grindCurrent) {
		log.Printf("this is grind version %s, but the server requires %s or higher", CurrentVersion.Version, server.GrindVersionRequired)
		log.Fatalf("  you must upgrade to continue")
	}
	grindRecommended := semver.MustParse(server.GrindVersionRecommended)
	if grindRecommended.GT(grindCurrent) {
		log.Printf("this is grind version %s, but the server recommends %s or higher", CurrentVersion.Version, server.GrindVersionRecommended)
		log.Printf("  please upgrade as soon as possible")
	}
}
