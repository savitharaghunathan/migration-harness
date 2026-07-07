package detect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manifests struct {
	PomXML          bool `json:"pom_xml"`
	PackageJSON     bool `json:"package_json"`
	PyprojectTOML   bool `json:"pyproject_toml"`
	RequirementsTXT bool `json:"requirements_txt"`
	SetupPY         bool `json:"setup_py"`
	GoMod           bool `json:"go_mod"`
	CargoTOML       bool `json:"cargo_toml"`
	Gemfile         bool `json:"gemfile"`
}

type FileCounts struct {
	Java       int `json:"java"`
	Python     int `json:"python"`
	JavaScript int `json:"javascript"`
	TypeScript int `json:"typescript"`
	Go         int `json:"go"`
	Rust       int `json:"rust"`
	CSharp     int `json:"csharp"`
	Ruby       int `json:"ruby"`
}

type GraphStats struct {
	Nodes       int `json:"nodes"`
	Edges       int `json:"edges"`
	Communities int `json:"communities"`
	GodNodes    int `json:"god_nodes"`
}

type Summary struct {
	Repo      string     `json:"repo"`
	Manifests Manifests  `json:"manifests"`
	Files     FileCounts `json:"files"`
	Graph     GraphStats `json:"graph"`
	GraphFile string     `json:"graph_file"`
}

// DetectManifests checks which build manifest files exist at repoDir's root.
func DetectManifests(repoDir string) Manifests {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(repoDir, name))
		return err == nil
	}
	return Manifests{
		PomXML:          exists("pom.xml"),
		PackageJSON:     exists("package.json"),
		PyprojectTOML:   exists("pyproject.toml"),
		RequirementsTXT: exists("requirements.txt"),
		SetupPY:         exists("setup.py"),
		GoMod:           exists("go.mod"),
		CargoTOML:       exists("Cargo.toml"),
		Gemfile:         exists("Gemfile"),
	}
}

type graphNode struct {
	SourceFile string `json:"source_file"`
	Degree     int    `json:"degree"`
}

type graphCommunity struct {
	ID int `json:"id"`
}

type graphDoc struct {
	Nodes       []graphNode       `json:"nodes"`
	Links       []json.RawMessage `json:"links"`
	Communities []graphCommunity  `json:"communities"`
}

// Summarize parses graphify's graph.json and produces the detect.json
// summary: manifest flags, per-language file counts, and graph stats.
// God nodes are nodes with degree > 20 (matches the existing bash heuristic).
func Summarize(repoDir string, graphJSON []byte) (Summary, error) {
	var g graphDoc
	if err := json.Unmarshal(graphJSON, &g); err != nil {
		return Summary{}, fmt.Errorf("parse graph.json: %w", err)
	}

	communitySet := map[int]bool{}
	for _, c := range g.Communities {
		communitySet[c.ID] = true
	}

	var files FileCounts
	var godNodes int
	for _, n := range g.Nodes {
		switch {
		case strings.HasSuffix(n.SourceFile, ".java"):
			files.Java++
		case strings.HasSuffix(n.SourceFile, ".py"):
			files.Python++
		case strings.HasSuffix(n.SourceFile, ".js"), strings.HasSuffix(n.SourceFile, ".jsx"):
			files.JavaScript++
		case strings.HasSuffix(n.SourceFile, ".ts"), strings.HasSuffix(n.SourceFile, ".tsx"):
			files.TypeScript++
		case strings.HasSuffix(n.SourceFile, ".go"):
			files.Go++
		case strings.HasSuffix(n.SourceFile, ".rs"):
			files.Rust++
		case strings.HasSuffix(n.SourceFile, ".cs"):
			files.CSharp++
		case strings.HasSuffix(n.SourceFile, ".rb"):
			files.Ruby++
		}
		if n.Degree > 20 {
			godNodes++
		}
	}

	return Summary{
		Repo:      repoDir,
		Manifests: DetectManifests(repoDir),
		Files:     files,
		Graph: GraphStats{
			Nodes:       len(g.Nodes),
			Edges:       len(g.Links),
			Communities: len(communitySet),
			GodNodes:    godNodes,
		},
		GraphFile: "graph.json",
	}, nil
}
