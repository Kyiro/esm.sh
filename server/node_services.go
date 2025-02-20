package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
)

// allowlist for require mode when parsing cjs exports fails
var requireModeAllowList = []string{
	"@babel/types",
	"cheerio",
	"graceful-fs",
	"he",
	"jsbn",
	"netmask",
	"xml2js",
	"keycode",
	"lru_map",
	"lz-string",
	"maplibre-gl",
	"pako",
	"postcss-selector-parser",
	"react-draggable",
	"resolve",
	"safe-buffer",
	"seedrandom",
	"stream-browserify",
	"stream-http",
	"typescript",
	"vscode-oniguruma",
	"web-streams-ponyfill",
}

const nsApp = `
const fs = require("fs");
const http = require("http");
const services = require("esm-node-services");

const requestListener = function (req, res) {
  if (req.method === "GET") {
    res.writeHead(200);
    res.end("READY");
  } else if (req.method === "POST") {
    let data = "";
    req.on("data", chunk => {
      data += chunk;
    });
    req.on("end", async () => {
      try {
        const { service, input } = JSON.parse(data);
        let output = null
        if (typeof service === "string" && service in services) {
          output = await services[service](input)
        } else {
          output = { error: 'service "' + service + '" not found' }
        }
        res.writeHead(output.error ? 400 : 200);
        res.end(JSON.stringify(output));
      } catch (e) {
        res.writeHead(500);
        res.end(JSON.stringify({ error: e.message, stack: e.stack }));
      }
    });
  } else {
    res.writeHead(405);
    res.end("Method not allowed");
  }
}

fs.writeFile("%s", process.pid.toString(), () => {});

const server = http.createServer(requestListener);
server.listen(%d);
`

var nsPidFile string

type NSPlayload struct {
	Service string                 `json:"service"`
	Input   map[string]interface{} `json:"input"`
}

func invokeNodeService(serviceName string, input map[string]interface{}) (data []byte, err error) {
	task := &NSPlayload{
		Service: serviceName,
		Input:   input,
	}
	buf := new(bytes.Buffer)
	err = json.NewEncoder(buf).Encode(task)
	if err != nil {
		return
	}
	if cfg.NsPort == 0 {
		return nil, errors.New("node service port is not set")
	}
	res, err := http.Post(fmt.Sprintf("http://localhost:%d", cfg.NsPort), "application/json", buf)
	if err != nil {
		// kill current ns process to get new one
		kill(nsPidFile)
		return
	}
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	return
}

func startNodeServices() (err error) {
	if cfg.NsPort == 0 {
		return errors.New("node service port is not set")
	}

	wd := path.Join(cfg.WorkDir, "ns")
	err = ensureDir(wd)
	if err != nil {
		return err
	}

	// kill previous ns process if exists
	nsPidFile = path.Join(cfg.WorkDir, "ns", "ns.pid")
	kill(nsPidFile)

	// install services
	cmd := exec.Command("pnpm", "add", "esm-node-services@0.9.1")
	cmd.Dir = wd
	var output []byte
	output, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("install services: %v %s", err, string(output))
		return
	}

	// create ns script
	err = os.WriteFile(
		path.Join(wd, "ns.js"),
		[]byte(fmt.Sprintf(nsApp, nsPidFile, cfg.NsPort)),
		0644,
	)
	if err != nil {
		return
	}

	errBuf := bytes.NewBuffer(nil)
	cmd = exec.Command("node", "ns.js")
	cmd.Dir = wd
	cmd.Stderr = errBuf

	err = cmd.Start()
	if err != nil {
		return
	}

	log.Debug("node services process started, pid is", cmd.Process.Pid)

	// wait the process to exit
	err = cmd.Wait()
	if errBuf.Len() > 0 {
		err = errors.New(strings.TrimSpace(errBuf.String()))
	}
	return
}

type cjsExportsResult struct {
	Reexport      string   `json:"reexport,omitempty"`
	ExportDefault bool     `json:"exportDefault"`
	Exports       []string `json:"exports"`
	Error         string   `json:"error"`
	Stack         string   `json:"stack"`
}

func cjsLexer(buildDir string, importPath string, nodeEnv string) (ret cjsExportsResult, err error) {
	args := map[string]interface{}{
		"buildDir":   buildDir,
		"importPath": importPath,
		"nodeEnv":    nodeEnv,
	}

	/* workaround for edge cases that can't be parsed by cjsLexer correctly */
	for _, name := range requireModeAllowList {
		if importPath == name || strings.HasPrefix(importPath, name+"/") {
			args["requireMode"] = 1
			break
		}
	}

	data, err := invokeNodeService("parseCjsExports", args)
	if err != nil {
		return
	}

	err = json.Unmarshal(data, &ret)
	if err != nil {
		return
	}

	if ret.Error != "" {
		if ret.Stack == "unreachable" {
			// whoops, the cjs-lexer is down, let' kill current ns process to get new one
			kill(nsPidFile)
		}
		if ret.Stack != "" {
			log.Errorf("[ns] cjsLexer: %s\n---\n%s\n---", ret.Error, ret.Stack)
		} else {
			log.Errorf("[ns] cjsLexer: %s", ret.Error)
		}
	}
	return
}
