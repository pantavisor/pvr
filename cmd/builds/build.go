// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/libpvr/image"
)

// maxArtifact is the largest artifact object (one S3 upload); builds made
// before artifacts were objects carried at most legacyArtifactLimit.
const (
	maxArtifact         = 5 << 30
	legacyArtifactLimit = 15 << 20
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var digestImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var executionID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var appName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

type input struct {
	Project          string `json:"project"`
	Target           string `json:"target"`
	Mode             string `json:"mode"`
	LimitSeconds     int64  `json:"limit_seconds"`
	SimulatedSeconds int64  `json:"simulated_seconds,omitempty"`
	SimulateFailure  bool   `json:"simulate_failure,omitempty"`
	Image            string `json:"image,omitempty"`
	Platform         string `json:"platform,omitempty"`
	GitURL           string `json:"git_url,omitempty"`
	GitRef           string `json:"git_ref,omitempty"`
	Dockerfile       string `json:"dockerfile,omitempty"`
	Context          string `json:"context,omitempty"`
	Secret           string `json:"secret,omitempty"`
	SigningSecret    string `json:"signing_secret,omitempty"`
	App              string `json:"app,omitempty"`
	// OEM is the OEM configuration part, as JSON from --oem.
	OEM json.RawMessage `json:"oem,omitempty"`
	// ImageBase builds meta-pantavisor's image base instead of an app.
	ImageBase *imageBase `json:"image_base,omitempty"`
	// DeviceImage is what an image build (mode image) assembles.
	DeviceImage *deviceImage `json:"device_image,omitempty"`
}

type imageBase struct {
	Kas []string `json:"kas"`
}

type deviceImage struct {
	Base        string `json:"base,omitempty"`
	Factory     string `json:"factory,omitempty"`
	MakeFactory bool   `json:"make_factory,omitempty"`
	// Release is a meta-pantavisor CI release's full image to remix, for
	// the default OEM: no image base build.
	Release *imageRelease `json:"release,omitempty"`
	// Identity is the signing identity whose trust the images get; none
	// is the builder's default OEM (pv-oem-developer-001, public developer
	// keys, development devices only).
	Identity     string       `json:"identity,omitempty"`
	Apps         []string     `json:"apps,omitempty"`
	Device       *imageDevice `json:"device,omitempty"`
	Policies     []string     `json:"policies,omitempty"`
	SecureBoot   string       `json:"secureboot,omitempty"`
	PantahubHost string       `json:"pantahub_host,omitempty"`
	// GroupTimeouts set device.json group timeouts in the BSP the platform
	// key signs: of a factory build or an image built from a base.
	GroupTimeouts map[string]int `json:"group_timeouts,omitempty"`
	// AutojoinSecret and WiFi.PasswordSecret name config secrets the
	// builder reads; their values never pass through pvr.
	AutojoinSecret string     `json:"autojoin_secret,omitempty"`
	WiFi           *imageWiFi `json:"wifi,omitempty"`
}

// imageWiFi is the network the images join; open without a password.
type imageWiFi struct {
	SSID           string `json:"ssid"`
	PasswordSecret string `json:"password_secret,omitempty"`
}

type imageRelease struct {
	Version string `json:"version"`
	Board   string `json:"board"`
}

type imageDevice struct {
	Device   string `json:"device"`
	Revision int    `json:"revision"`
}

// imageFlags are the flags of an image build; deviceImageFrom reads them.
var imageFlags = []string{"base", "factory", "release", "make-factory", "identity", "app-build", "device", "policy", "secureboot", "pantahub-host", "group-timeout",
	"autojoin-secret", "wifi-ssid", "wifi-password-secret"}

func deviceImageFrom(c *cli.Context) (*deviceImage, error) {
	img := &deviceImage{Base: c.String("base"), Factory: c.String("factory"), MakeFactory: c.Bool("make-factory"), Identity: c.String("identity"), Apps: c.StringSlice("app-build"),
		Policies: c.StringSlice("policy"), SecureBoot: c.String("secureboot"), PantahubHost: c.String("pantahub-host")}
	timeouts, err := image.ParseGroupTimeouts(c.StringSlice("group-timeout"))
	if err != nil {
		return nil, err
	}
	if len(timeouts) > 0 && img.Factory != "" {
		return nil, errors.New("--group-timeout changes the BSP's device.json: give it to the factory build or to an image built from --base")
	}
	img.GroupTimeouts = timeouts
	if r := c.String("release"); r != "" {
		version, board, ok := strings.Cut(r, "/")
		if !ok || version == "" || board == "" || strings.Contains(board, "/") {
			return nil, errors.New("--release is VERSION/BOARD of a meta-pantavisor CI release, such as 030/rpi-scarthgap (see https://pantavisor.io/downloads)")
		}
		img.Release = &imageRelease{Version: version, Board: board}
	}
	sources := 0
	for _, set := range []bool{img.Base != "", img.Factory != "", img.Release != nil} {
		if set {
			sources++
		}
	}
	if sources != 1 || (img.Base != "" && !executionID.MatchString(img.Base)) || (img.Factory != "" && !executionID.MatchString(img.Factory)) {
		return nil, errors.New("--base (an image base build), --factory (a factory build) or --release (a meta-pantavisor CI release) is needed, one of them")
	}
	if img.Release != nil && ((img.Identity != "" && img.Identity != image.DefaultOEMName) || img.MakeFactory || len(img.Policies) > 0 || len(timeouts) > 0) {
		return nil, errors.New("--release images are of the default OEM (" + image.DefaultOEMName + ") and keep the release's initrd: no --identity, --policy, --make-factory or --group-timeout; those need --base")
	}
	if img.MakeFactory && (img.Factory != "" || len(img.Apps) > 0 || c.String("device") != "" || c.String("oem") != "" ||
		c.IsSet("autojoin-secret") || c.IsSet("wifi-ssid") || c.IsSet("wifi-password-secret")) {
		return nil, errors.New("--make-factory makes secret-free factory images of --base: no apps, device, oem, autojoin or wifi")
	}
	if c.IsSet("identity") && !identityName.MatchString(img.Identity) {
		return nil, errors.New("--identity names a signing identity with a platform key (see pvr signing ls)")
	}
	if c.IsSet("autojoin-secret") {
		if img.AutojoinSecret = c.String("autojoin-secret"); !secretName.MatchString(img.AutojoinSecret) {
			return nil, errors.New("--autojoin-secret names the config secret holding the join token (see pvr secret ls)")
		}
	}
	ssid, password := c.String("wifi-ssid"), c.String("wifi-password-secret")
	if c.IsSet("wifi-ssid") && (ssid == "" || len(ssid) > 32 || strings.ContainsAny(ssid, "\n\r\x00")) {
		return nil, errors.New("--wifi-ssid is 1 to 32 bytes, without newlines or NUL")
	}
	if c.IsSet("wifi-password-secret") && (ssid == "" || !secretName.MatchString(password)) {
		return nil, errors.New("--wifi-password-secret names the config secret holding the password of --wifi-ssid (see pvr secret ls)")
	}
	if ssid != "" {
		img.WiFi = &imageWiFi{SSID: ssid, PasswordSecret: password}
	}
	for _, id := range img.Apps {
		if !executionID.MatchString(id) {
			return nil, errors.New("--app-build is the id of a succeeded app build")
		}
	}
	if ref := c.String("device"); ref != "" {
		device, rev, ok := strings.Cut(ref, "@")
		n, err := strconv.Atoi(rev)
		if !ok || device == "" || err != nil || n < 0 {
			return nil, errors.New("--device is <device id or nick>@<revision>")
		}
		img.Device = &imageDevice{Device: device, Revision: n}
	}
	return img, nil
}

type execution struct {
	Children       []json.RawMessage `json:"children"`
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	ArtifactSHA256 string            `json:"artifact_sha256"`
}

type client struct {
	session *libpvr.Session
	base    string
}

func newClient(c *cli.Context) (*client, error) {
	base, _ := c.App.Metadata["PVR_BASEURL"].(string)
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("build baseurl must be an API origin without credentials, path, query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("build API requires HTTPS (HTTP is allowed on loopback)")
	}
	resty.SetRedirectPolicy(resty.NoRedirectPolicy())
	resty.SetTimeout(30 * time.Second)
	session, err := libpvr.NewSession(c.App)
	if err != nil {
		return nil, err
	}
	return &client{session: session, base: strings.TrimRight(base, "/")}, nil
}

func (h *client) call(method, path string, body interface{}, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := h.session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method, req.URL = method, h.base+path
		req.SetContext(ctx)
		if body != nil {
			req.SetHeader("Content-Type", "application/json")
			req.SetBody(body)
		}
		if key != "" {
			req.SetHeader("Idempotency-Key", key)
		}
		return nil
	})
	if err != nil {
		return nil, errors.New("build API request failed; check connectivity, TLS trust and pvr login")
	}
	if resp == nil {
		return nil, errors.New("build API returned no response")
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, apiError(resp.StatusCode(), resp.Body())
	}
	if len(resp.Body()) > 1<<20 {
		return nil, errors.New("build API JSON exceeds 1 MiB")
	}
	if !json.Valid(resp.Body()) {
		return nil, errors.New("build API returned invalid JSON")
	}
	return resp.Body(), nil
}

// apiError names the status and, when the API explained it, why.
// callNoContent is call for answers without a JSON body, such as a delete.
func (h *client) callNoContent(method, path string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := h.session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method, req.URL = method, h.base+path
		req.SetContext(ctx)
		return nil
	})
	if err != nil || resp == nil {
		return 0, errors.New("API request failed; check connectivity, TLS trust and pvr login")
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return resp.StatusCode(), apiError(resp.StatusCode(), resp.Body())
	}
	return resp.StatusCode(), nil
}

func jsonUnmarshal(data []byte, out interface{}) error {
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("the API answered unexpected JSON")
	}
	return nil
}

func apiError(status int, body []byte) error {
	var explained struct {
		Message string `json:"message"`
		Error   string `json:"Error"`
	}
	_ = json.Unmarshal(body, &explained)
	reason := explained.Message
	if reason == "" {
		reason = explained.Error
	}
	if len(reason) > 300 || strings.ContainsAny(reason, "\r\n") {
		reason = ""
	}
	hint := ""
	switch status {
	case 401, 403:
		hint = "; log in to this Hub and use builds read/write scopes"
	case 404:
		hint = "; execution not found or not owned by this account"
	case 409:
		hint = "; execution state or idempotency key conflicts"
	case 501:
		hint = "; development executions are disabled or this operation is not implemented"
	}
	if reason != "" {
		return fmt.Errorf("API: HTTP %d (%s): %s", status, http.StatusText(status), reason)
	}
	return fmt.Errorf("API: HTTP %d (%s)%s", status, http.StatusText(status), hint)
}

func printJSON(c *cli.Context, data []byte) error {
	var pretty strings.Builder
	// Encode through a raw message to preserve integers and unknown fields.
	enc := json.NewEncoder(&pretty)
	enc.SetIndent("", "  ")
	if err := enc.Encode(json.RawMessage(data)); err != nil {
		return err
	}
	_, err := io.WriteString(c.App.Writer, pretty.String())
	return err
}

func pathFor(c *cli.Context) (string, error) {
	if c.NArg() != 1 || !executionID.MatchString(c.Args().First()) {
		return "", errors.New("provide one execution ID (64 lowercase hex characters)")
	}
	return "/builds/executions/" + c.Args().First(), nil
}

func simple(method, suffix string) func(*cli.Context) error {
	return func(c *cli.Context) error {
		path, err := pathFor(c)
		if err != nil {
			return err
		}
		h, err := newClient(c)
		if err != nil {
			return err
		}
		var body interface{}
		if method == http.MethodPost {
			body = map[string]interface{}{}
		}
		data, err := h.call(method, path+suffix, body, "")
		if err != nil {
			return err
		}
		return printJSON(c, data)
	}
}

func start(c *cli.Context) error {
	if c.NArg() != 0 {
		return errors.New("start accepts flags only; use --project and --target")
	}
	in := input{Project: c.String("project"), Target: c.String("target"), Mode: c.String("mode"), LimitSeconds: c.Int64("limit-seconds")}
	if c.String("git-url") != "" && !c.IsSet("mode") {
		in.Mode = "git"
	}
	if (c.String("base") != "" || c.String("factory") != "" || c.String("release") != "") && !c.IsSet("mode") {
		in.Mode = "image"
	}
	for _, flag := range imageFlags {
		if c.IsSet(flag) && in.Mode != "image" {
			return fmt.Errorf("--%s goes with an image build (--mode image)", flag)
		}
	}
	if c.IsSet("image-base") && in.Mode != "git" {
		return errors.New("--image-base goes with a git build of meta-pantavisor")
	}
	if !c.IsSet("limit-seconds") {
		// What each kind of build takes: kas may build for hours, an
		// image is assembled in minutes.
		switch {
		case c.IsSet("image-base"):
			in.LimitSeconds = 4 * 3600
		case in.Mode == "image":
			in.LimitSeconds = 1800
		}
	}
	if in.Mode != "git" && (c.String("git-url") != "" || c.IsSet("git-ref") || c.IsSet("dockerfile") || c.IsSet("context") || c.IsSet("secret")) {
		return errors.New("Git source flags require git mode")
	}
	if !identifier.MatchString(in.Project) || !identifier.MatchString(in.Target) {
		return errors.New("project and target must be identifiers")
	}
	// A git build may be a Yocto or Buildroot recipe: up to four hours.
	limit := int64(3600)
	if in.Mode == "git" {
		limit = 4 * 3600
	}
	if in.LimitSeconds < 1 || in.LimitSeconds > limit {
		return fmt.Errorf("development limit-seconds must be 1..%d for a %s build", limit, in.Mode)
	}
	if in.App = c.String("app"); in.App != "" && (in.Mode == "simulated" || !appName.MatchString(in.App) || in.App == "bsp") {
		return errors.New("app names the container of a docker or git build: lowercase letters, digits, '.', '_' or '-', not bsp")
	}
	if in.SigningSecret = c.String("signing-secret"); in.SigningSecret != "" && (in.Mode == "simulated" || !secretName.MatchString(in.SigningSecret)) {
		return errors.New("signing-secret names the pvs secret of the account that signs a docker or git build (see pvr secret ls)")
	}
	if path := c.String("oem"); path != "" {
		if in.Mode == "simulated" {
			return errors.New("oem adds a part to a docker, git or image build")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading --oem: %w", err)
		}
		if len(raw) > 128<<10 || !json.Valid(raw) || !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			return errors.New("--oem is a JSON object of at most 128 KiB: name, configs, files, signing_secret")
		}
		in.OEM = raw
	}
	switch in.Mode {
	case "simulated":
		if c.IsSet("platform") {
			in.Platform = c.String("platform")
			if !supportedPlatform(in.Platform) {
				return errors.New("unsupported target platform")
			}
		}
		in.SimulatedSeconds = c.Int64("simulated-seconds")
		in.SimulateFailure = c.Bool("simulate-failure")
		if in.SimulatedSeconds < 1 || in.SimulatedSeconds > 3600 || c.String("image") != "" {
			return errors.New("invalid simulation duration or image")
		}
	case "docker":
		in.Image, in.Platform = c.String("image"), c.String("platform")
		if !digestImage.MatchString(in.Image) || !supportedPlatform(in.Platform) {
			return errors.New("docker mode needs --image pinned by sha256 and --platform linux/amd64, linux/arm64 or linux/arm/v6")
		}
		if c.IsSet("simulated-seconds") || c.Bool("simulate-failure") {
			return errors.New("simulation flags require simulated mode")
		}
	case "image":
		img, err := deviceImageFrom(c)
		if err != nil {
			return err
		}
		in.DeviceImage = img
		if c.IsSet("platform") {
			if in.Platform = canonicalPlatform(c.String("platform")); !supportedPlatform(in.Platform) {
				return errors.New("--platform picks the exports of app builds: linux/amd64, linux/arm64 or linux/arm/v6")
			}
		}
		if in.App != "" || in.SigningSecret != "" || c.String("image") != "" || c.IsSet("simulated-seconds") || c.Bool("simulate-failure") {
			return errors.New("an image build signs with its identity's platform key: no app, signing-secret, image or simulation flags")
		}
	case "git":
		in.GitURL, in.GitRef, in.Dockerfile, in.Context, in.Platform = c.String("git-url"), c.String("git-ref"), c.String("dockerfile"), c.String("context"), c.String("platform")
		if kas := c.String("image-base"); kas != "" {
			in.ImageBase = &imageBase{}
			for _, f := range strings.Split(kas, ",") {
				if f = strings.TrimSpace(f); f != "" {
					in.ImageBase.Kas = append(in.ImageBase.Kas, f)
				}
			}
			if in.App != "" || in.SigningSecret != "" || in.OEM != nil {
				return errors.New("an image base is unsigned and has no app or oem part")
			}
		}
		in.Secret = c.String("secret")
		if in.Secret != "" && !secretName.MatchString(in.Secret) {
			return errors.New("secret is the name of an account secret (see pvr secret ls)")
		}
		parsed, parseErr := url.Parse(in.GitURL)
		if parseErr != nil || parsed.Scheme != "https" || (parsed.Host != "github.com" && parsed.Host != "gitlab.com") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("git-url must be an HTTPS GitHub or GitLab repository URL")
		}
		if c.String("image") != "" || c.IsSet("simulated-seconds") || c.Bool("simulate-failure") || !supportedPlatform(in.Platform) {
			return errors.New("git mode needs a supported platform without registry/simulation flags")
		}
	default:
		return errors.New("mode must be simulated, docker, git or image")
	}
	key := c.String("idempotency-key")
	if key == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		key = hex.EncodeToString(b[:])
	}
	if len(key) > 128 || strings.ContainsAny(key, "\r\n") {
		return errors.New("idempotency-key must be at most 128 bytes without newlines")
	}
	// Print before submission: users can safely reconcile an ambiguous response
	// by replaying this key, instead of unintentionally submitting another job.
	stderr := c.App.ErrWriter
	if stderr == nil {
		stderr = os.Stderr
	}
	fmt.Fprintln(stderr, "Idempotency-Key: "+key)
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodPost, "/builds/executions", in, key)
	if err != nil {
		return err
	}
	var doc execution
	if err = json.Unmarshal(data, &doc); err != nil || !executionID.MatchString(doc.ID) {
		return errors.New("build API returned an invalid execution ID")
	}
	return printJSON(c, data)
}

func download(c *cli.Context) error {
	path, err := pathFor(c)
	if err != nil {
		return err
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	data, err := h.call(http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	var doc execution
	if err = json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Status != "succeeded" && len(doc.Children) == 0 {
		return errors.New("artifacts require a succeeded execution")
	}
	if !executionID.MatchString(doc.ArtifactSHA256) {
		return errors.New("build API returned an invalid artifact checksum")
	}
	bearer, _ := c.App.Metadata["PVR_AUTH"].(string)
	if bearer == "" {
		return errors.New("artifact download requires a pvr login or access token")
	}
	// The status call has refreshed/authenticated the session. Use the same
	// transport (CA, TLS and proxy settings), without redirects.
	transport := resty.DefaultClient.GetClient().Transport
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: noRedirects}
	req, err := http.NewRequest(http.MethodGet, h.base+path+"/artifacts", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("artifact download failed; check connectivity and TLS trust")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp.StatusCode, nil)
	}

	output := c.String("output")
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		// Builds made before artifacts were account objects.
		if output == "" {
			output = "artifacts.tar.gz"
		}
		if err = saveArtifact(resp.Body, output, doc.ArtifactSHA256, legacyArtifactLimit, false); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.App.Writer, output)
		return err
	}

	// Artifacts are objects of the account: find the one to fetch, then
	// download it from its signed URL, which takes no credentials.
	var listed struct {
		Artifacts []artifact `json:"artifacts"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listed); err != nil {
		return errors.New("build API returned an invalid artifact list")
	}
	chosen, err := chooseArtifact(listed.Artifacts, c.String("name"), c.String("platform"))
	if err != nil {
		return err
	}
	if output == "" {
		output = chosen.Name
		if len(doc.Children) > 0 && chosen.Platform != "" {
			output = strings.ReplaceAll(chosen.Platform, "/", "-") + "-" + output
		}
	}
	objectData, err := h.call(http.MethodGet, "/objects/"+chosen.SHA256, nil, "")
	if err != nil {
		return err
	}
	var object struct {
		SignedGetURL string `json:"signed-geturl"`
	}
	if err := json.Unmarshal(objectData, &object); err != nil {
		return errors.New("objects API returned no download URL")
	}
	target, err := url.Parse(object.SignedGetURL)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" || target.User != nil {
		return errors.New("objects API returned an invalid download URL")
	}
	// Large images take long: 5 minutes, plus a second per MiB. The signed
	// URL carries no credentials and storage may redirect it (production
	// answers 302), so redirects are followed, but never from https to http.
	blobClient := &http.Client{Transport: transport, Timeout: 5*time.Minute + time.Duration(chosen.Size/(1<<20))*time.Second, CheckRedirect: signedRedirects}
	blob, err := blobClient.Get(target.String())
	if err != nil {
		return errors.New("artifact download failed; check connectivity and TLS trust")
	}
	defer blob.Body.Close()
	if blob.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact download: HTTP %d", blob.StatusCode)
	}
	if err = saveArtifact(blob.Body, output, chosen.SHA256, chosen.Size, true); err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.App.Writer, output)
	return err
}

// signedRedirects lets a signed download URL redirect, as object storage
// does, up to 10 times and never from https to plain http.
func signedRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("artifact download: too many redirects")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("artifact download: redirect leaves https")
	}
	return nil
}

// artifact is a build output stored as an object of the account.
type artifact struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

var artifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func chooseArtifact(list []artifact, name, platform string) (artifact, error) {
	platform = canonicalPlatform(strings.TrimSpace(platform))
	var found []artifact
	for _, a := range list {
		if (name == "" || a.Name == name) && (platform == "" || a.Platform == platform) {
			found = append(found, a)
		}
	}
	switch {
	case len(found) == 0 && platform != "":
		return artifact{}, fmt.Errorf("the pipeline has no completed artifact for %q", platform)
	case len(found) == 0 && name != "":
		return artifact{}, fmt.Errorf("the build has no artifact named %q", name)
	case len(found) == 0:
		return artifact{}, errors.New("the build has no artifacts")
	case len(found) > 1:
		return artifact{}, errors.New("the pipeline has several artifacts; choose one with --platform or --name")
	}
	a := found[0]
	if !artifactName.MatchString(a.Name) || !executionID.MatchString(a.SHA256) || a.Size < 1 || a.Size > maxArtifact {
		return artifact{}, errors.New("build API returned an invalid artifact")
	}
	return a, nil
}

// saveArtifact writes exactly size bytes whose sha256 is want to a new file.
func saveArtifact(source io.Reader, destination, want string, size int64, exact bool) error {
	f, err := os.CreateTemp(filepath.Dir(destination), ".pvr-build-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(source, size+1))
	if err != nil {
		return err
	}
	if n > size {
		return fmt.Errorf("artifact exceeds %d bytes", size)
	}
	if exact && n != size {
		return fmt.Errorf("artifact size mismatch: %d != %d", n, size)
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return errors.New("artifact checksum mismatch")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Publish atomically without overwriting an existing file or symlink.
	if err = os.Link(name, destination); err != nil {
		return fmt.Errorf("cannot create artifact output (existing files are never overwritten): %w", err)
	}
	return nil
}

// CommandBuild manages Builder executions through the authenticated Hub API.
func CommandBuild() cli.Command {
	return cli.Command{Name: "build", Usage: "start, inspect and download remote Pantavisor builds",
		Description: "Development preview: simulated executions, registry exports and Git Dockerfile builds; private repositories are fetched with account secrets (pvr secret). Uses the global --baseurl and pvr login. Yocto builds are pending.",
		Subcommands: []cli.Command{
			{Name: "start", Usage: "submit a development build; prints execution JSON", Action: start, Flags: []cli.Flag{
				cli.StringFlag{Name: "project", Usage: "project identifier"}, cli.StringFlag{Name: "target", Value: "rpi4", Usage: "target identifier"},
				cli.StringFlag{Name: "mode", Value: "simulated", Usage: "simulated, docker (registry export), git (inferred with git-url) or image (inferred with base)"},
				cli.Int64Flag{Name: "limit-seconds", Value: 60, Usage: "runner execution ceiling per architecture (1..3600; 1..14400 for git, whose pvpkg.json may build a Yocto or Buildroot recipe)"},
				cli.Int64Flag{Name: "simulated-seconds", Value: 8, Usage: "simulation duration"}, cli.BoolFlag{Name: "simulate-failure", Usage: "simulate a failed build"},
				cli.StringFlag{Name: "image", Usage: "registry image pinned by sha256 digest"}, cli.StringFlag{Name: "platform", Value: "linux/arm64", Usage: "one architecture or comma-separated pipeline targets (amd64,arm64,arm32v6)"},
				cli.StringFlag{Name: "git-url", Usage: "HTTPS GitHub/GitLab repository; private ones use the account secret for that host"},
				cli.StringFlag{Name: "secret", Usage: "account secret to fetch a private repository with (default: the one for its host, see pvr secret ls, for the repository only); only a secret named here also reaches a Yocto or Buildroot recipe's own fetches"},
				cli.StringFlag{Name: "signing-secret", Usage: "account pvs secret to sign the export with; only a key named here signs (default: pvr's public developer key, development only)"},
				cli.StringFlag{Name: "oem", Usage: "JSON file of an OEM configuration part to add, signed with the identity's platform key: {\"configs\": {\"prod\": {\"PH_FACTORY_AUTOTOK\": {\"secret\": \"prod-autotok\"}}}}; secret values come from config secrets (pvr secret add --type config)"},
				cli.StringFlag{Name: "git-ref", Value: "main", Usage: "branch, tag or exact 40-character commit"},
				cli.StringFlag{Name: "dockerfile", Value: "Dockerfile", Usage: "Dockerfile path relative to repository root"},
				cli.StringFlag{Name: "context", Value: ".", Usage: "build context relative to repository root"},
				cli.StringFlag{Name: "app", Usage: "container name in the export (<app>.pvrexport.tgz); default: the repository or image name"},
				cli.StringFlag{Name: "idempotency-key", Usage: "stable submission key; generated and printed when omitted"},
				cli.StringFlag{Name: "image-base", Usage: "git build of meta-pantavisor: build its image base with these comma-separated kas files instead of an app (see the Hub's /docs/guide/builds-images)"},
				cli.StringFlag{Name: "base", Usage: "image build: id of the image base build (sets --mode image)"},
				cli.StringFlag{Name: "release", Usage: "image build: VERSION/BOARD of a meta-pantavisor CI release to remix instead of --base, such as 030/rpi-scarthgap (sets --mode image): default OEM, no image base build"},
				cli.StringFlag{Name: "factory", Usage: "image build: id of a factory build of the identity to start from (sets --mode image): no BSP signing, seconds"},
				cli.BoolFlag{Name: "make-factory", Usage: "image build: make secret-free factory images of --base, one per --policy, for later image builds"},
				cli.StringFlag{Name: "identity", Usage: "image build: signing identity whose trust the images get; its platform key signs the BSP (default: the builder's pv-oem-developer-001, public developer keys, development devices only)"},
				cli.StringSliceFlag{Name: "app-build", Usage: "image build: id of an app build whose export joins the images; repeat for several"},
				cli.StringFlag{Name: "device", Usage: "image build: <device id or nick>@<revision> whose apps join the images"},
				cli.StringSliceFlag{Name: "policy", Usage: "image build: PV_POLICY of one image, such as prod; repeat for one image each"},
				cli.StringFlag{Name: "secureboot", Usage: "image build: PV_SECUREBOOT_MODE, strict (default), lenient, audit or disabled"},
				cli.StringFlag{Name: "pantahub-host", Usage: "image build: creds.host the devices join"},
				cli.StringSliceFlag{Name: "group-timeout", Usage: "image build: NAME=SECONDS, timeout of a container group of the BSP's device.json, such as root=120 (with --base); repeat for several"},
				cli.StringFlag{Name: "autojoin-secret", Usage: "image build: config secret holding the join token the devices claim themselves with (not with --make-factory)"},
				cli.StringFlag{Name: "wifi-ssid", Usage: "image build: Wi-Fi network the devices join, open unless --wifi-password-secret (not with --make-factory)"},
				cli.StringFlag{Name: "wifi-password-secret", Usage: "image build: config secret holding the password of --wifi-ssid"},
			}},
			{Name: "status", ArgsUsage: "<id>", Usage: "inspect execution status and metadata", Action: simple(http.MethodGet, "")},
			{Name: "logs", ArgsUsage: "<id>", Usage: "get the most recent log entries as JSON (polling snapshot)", Action: simple(http.MethodGet, "/logs")},
			{Name: "download", ArgsUsage: "<id>", Usage: "download a build artifact, checked against its sha256", Action: download, Flags: []cli.Flag{
				cli.StringFlag{Name: "output,o", Usage: "new output file, never overwritten (default: the artifact's name)"},
				cli.StringFlag{Name: "name", Usage: "artifact to download when a build has several"},
				cli.StringFlag{Name: "platform", Usage: "architecture to download from a multi-architecture pipeline"},
			}},
			{Name: "cancel", ArgsUsage: "<id>", Usage: "request cancellation; running builds finish cancellation after runner stop", Action: simple(http.MethodPost, "/cancel")},
			{Name: "list", Usage: "list recent executions", Flags: []cli.Flag{
				cli.StringFlag{Name: "project", Usage: "filter executions by project identifier"},
				cli.BoolFlag{Name: "artifacts-only", Usage: "only list executions that produced artifacts"},
			}, Action: func(c *cli.Context) error {
				if c.NArg() != 0 {
					return errors.New("list accepts no arguments")
				}
				h, err := newClient(c)
				if err != nil {
					return err
				}
				urlPath := "/builds/executions"
				if p := c.String("project"); p != "" {
					urlPath += "?project=" + url.QueryEscape(p)
				}
				data, err := h.call(http.MethodGet, urlPath, nil, "")
				if err != nil {
					return err
				}
				if c.Bool("artifacts-only") {
					var rawList []map[string]interface{}
					if err := json.Unmarshal(data, &rawList); err != nil {
						return err
					}
					var filtered []map[string]interface{}
					for _, item := range rawList {
						if sha, ok := item["artifact_sha256"].(string); ok && sha != "" {
							filtered = append(filtered, item)
						}
					}
					out, err := json.Marshal(filtered)
					if err != nil {
						return err
					}
					return printJSON(c, out)
				}
				return printJSON(c, data)
			}},
			{Name: "usage", Usage: "show development build usage and quota status", Action: func(c *cli.Context) error {
				if c.NArg() != 0 {
					return errors.New("usage accepts no arguments")
				}
				h, err := newClient(c)
				if err != nil {
					return err
				}
				data, err := h.call(http.MethodGet, "/builds/usage", nil, "")
				if err != nil {
					return err
				}
				return printJSON(c, data)
			}},
		}}
}

func supportedPlatform(platform string) bool {
	for _, target := range strings.Split(platform, ",") {
		switch canonicalPlatform(strings.TrimSpace(target)) {
		case "linux/amd64", "linux/arm64", "linux/arm/v6":
		default:
			return false
		}
	}
	return true
}

func canonicalPlatform(platform string) string {
	switch platform {
	case "amd64", "x86_64":
		return "linux/amd64"
	case "arm64":
		return "linux/arm64"
	case "arm32v6", "armv6":
		return "linux/arm/v6"
	}
	return platform
}
