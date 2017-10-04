package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coreos/go-oidc"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/oauth2"

	"encoding/json"
	"io/ioutil"

	. "github.com/logrusorgru/aurora"
	"runtime"
)

var logger = log.New(os.Stdout, "", log.LUTC)

type Configuration struct {
	Issuer      string   `json:"issuer"`
	RedirectURL string   `json:"redirectUrl"`
	LoginSecret string   `json:"loginSecret"`
	Cluster     string   `json:"cluster"`
	Aliases     []string `json:"aliases"`
}

func rawConfig() map[string]*Configuration {
	file, err := os.Open(os.Getenv("HOME") + "/.kubectl-login.json")
	if err != nil {
		logger.Fatal("error:", err)
	}
	data, err := ioutil.ReadAll(file)
	if err != nil {
		logger.Fatal("error:", err)
	}
	var objmap map[string]*Configuration
	err = json.Unmarshal(data, &objmap)
	if err != nil {
		logger.Fatal("error:", err)
	}

	return objmap
}

func main() {

	rawConfigMap := rawConfig()

	args := os.Args[1:]

	if len(args) == 0 {
		logger.Fatal(fmt.Sprintf("Alias is mandatory i.e %s. try '%s' to get this value.", Bold(Cyan("kubectl-login <ALIAS>")) , Bold(Cyan("cat $HOME/.kubectl-login.json"))))
	}

	alias := args[0]

	var config *Configuration
	var cluster string

	for k, v := range rawConfigMap {
		if containsAlias(v, alias) {
			cluster = k
			config = v
		}
	}

	if cluster == "" {
		logger.Fatal(fmt.Sprintf("Alias \"%s\" not found. try '%s' to get this value.", Bold(Cyan(alias)) , Bold(Cyan("cat $HOME/.kubectl-login.json"))))
	}

	var kl string

	if os.Getenv("KUBELOGIN") != "" {
		kl = os.Getenv("KUBELOGIN")
	} else if config.LoginSecret != "" {
		kl = config.LoginSecret
	} else {
		logger.Fatal("KUBELOGIN is not set. You Can also set this in your ~/.kubectl-login.json file.")
	}

	ctx := context.Background()
	// Initialize a provider by specifying dex's issuer URL.
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		logger.Fatal(err)
	}

	// Configure the OAuth2 config with the client values.
	oauth2Config := oauth2.Config{
		// client_id and client_secret of the client.
		ClientID:     "kubectl-login",
		ClientSecret: kl,

		// The redirectURL.
		RedirectURL: config.RedirectURL,

		// Discovery returns the OAuth2 endpoints.
		Endpoint: provider.Endpoint(),

		// "openid" is a required scope for OpenID Connect flows.
		//
		// Other scopes, such as "groups" can be requested.
		Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}

	// Create an ID token parser.

	idTokenVerifier := provider.Verifier(&oidc.Config{ClientID: "kubectl-login"})

	acu := oauth2Config.AuthCodeURL("some state")

	var openCmd string

	if runtime.GOOS == "darwin" {
		openCmd = "open"
	} else {
		openCmd = "sensible-browser"
	}

	cmd := exec.Command(openCmd, acu)
	err = cmd.Start()
	if err != nil {
		logger.Fatal(err)
	}

	rawToken := getToken()

	_, err = idTokenVerifier.Verify(ctx, rawToken)

	if err != nil {
		logger.Printf("token is invalid, error: %s\n", err.Error())
		return
	}

	setCreds(rawToken)
	switchContext(cluster)
	notifyAndPrompt()
}

func containsAlias(c *Configuration, s string) bool {
	for _, val := range c.Aliases {
		if val == s {
			return true
		}
	}
	return false
}

func notifyAndPrompt() {
	fmt.Printf("\nLogged in. Now try `%s` to get your context or '%s' to get started.\n", Cyan("kubectl config get-contexts"), Cyan("kubectl get pods"))
}

func getToken() string {
	fmt.Print(Cyan("Enter token: "))

	// handle restoring terminal
	stdinFd := int(os.Stdin.Fd())
	state, err := terminal.GetState(stdinFd)
	defer terminal.Restore(stdinFd, state)

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt)
	go func() {
		for _ = range sigch {
			terminal.Restore(stdinFd, state)
			os.Exit(1)
		}
	}()

	byteToken, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		logger.Fatal(err)
	}
	token := string(byteToken)

	return strings.TrimSpace(token)
}

func setCreds(token string) {
	tstr := fmt.Sprintf("--token=%s", token)
	cmd := exec.Command("kubectl", "config", "set-credentials", "kubectl-login", tstr)
	err := cmd.Run()
	if err != nil {
		logger.Fatal(err)
	}
}

func switchContext(cluster string) {

	clusterArg := fmt.Sprintf("--cluster=%s", cluster)
	cmd := exec.Command("kubectl", "config", "set-context", "kubectl-login-context", "--user=kubectl-login", clusterArg, "--namespace=default")
	err := cmd.Run()
	if err != nil {
		logger.Fatal(err)
	}

	cmd = exec.Command("kubectl", "config", "use-context", "kubectl-login-context")
	err = cmd.Run()
	if err != nil {
		logger.Fatal(err)
	}
}
