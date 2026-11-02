package webui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"reflect"
	"testing"
)

// Exercise the same multipart contract as folder selection, ZIP upload and tar upload.
func bundleUpload(t *testing.T, format string, files map[string]string) ([]byte, string) {
	t.Helper()
	if format == "zip" {
		return multipartZip(t, files)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if format == "folder" {
		for name, content := range files {
			w, err := mw.CreateFormFile("files", path.Base(name))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := mw.WriteField("paths", name); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o644, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		w, err := mw.CreateFormFile("file", "bundle.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(archive.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), mw.FormDataContentType()
}

func TestSkillBundleUploadAndImport(t *testing.T) {
	for _, format := range []string{"folder", "zip", "tar.gz"} {
		for _, prefix := range []string{"", "download/"} {
			t.Run(format+"/"+prefix, func(t *testing.T) {
				c := login(t)
				md := "---\nname: query-helper\ndescription: Query a test service\n---\nUse helper.py.\n"
				files := map[string]string{
					prefix + "README.md":                     "Outer package documentation",
					prefix + ".claude-plugin/plugin.json":    `{"name":"query-plugin","skills":"./skills"}`,
					prefix + ".DS_Store":                     "ignored",
					prefix + "skills/.DS_Store":              "ignored",
					prefix + "skills/query-helper/SKILL.md":  md,
					prefix + "skills/query-helper/helper.py": "print('fixture only')\n",
					prefix + "hooks/hooks.json":              `{"hooks":{}}`,
				}
				body, ct := bundleUpload(t, format, files)
				rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
				if rr.Code != 200 {
					t.Fatalf("upload %d: %s", rr.Code, rr.Body.String())
				}
				var p skillPreview
				if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if p.SourceRoot != prefix+"skills/query-helper" || p.Name != "query-helper" || p.Files != 2 || p.Existing != nil {
					t.Fatalf("preview %+v", p)
				}
				if len(p.Tree) != 2 || p.Tree[0].Path != "SKILL.md" || p.Tree[1].Path != "helper.py" {
					t.Fatalf("preview must contain only skill-relative files: %+v", p.Tree)
				}
				if code, out := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != 200 {
					t.Fatalf("import %d: %v", code, out)
				}
				list, err := c.s.deps.Skills.List(context.Background())
				if err != nil || len(list) != 1 || list[0].CurrentVersion == nil {
					t.Fatalf("stored skills %+v: %v", list, err)
				}
				got, err := c.s.deps.Skills.Files(context.Background(), list[0].CurrentVersion.ID)
				want := map[string][]byte{"SKILL.md": []byte(md), "helper.py": []byte("print('fixture only')\n")}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("imported files %v: %v", got, err)
				}
				if stages, err := os.ReadDir(c.s.stagingDir()); err != nil || len(stages) != 0 {
					t.Fatalf("committed stage not cleaned: %v %v", stages, err)
				}
				// Plugin wrappers retain the same explicit duplicate-name/version confirmation.
				rr = c.do(http.MethodPost, "/api/skills/upload", ct, body)
				if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil || rr.Code != 200 || p.Existing == nil || p.Existing.VersionNo != 1 {
					t.Fatalf("existing preview %d: %+v %v", rr.Code, p, err)
				}
				if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != 409 {
					t.Fatalf("duplicate import %d", code)
				}
				if code, _ := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name, "mode": "version"}); code != 200 {
					t.Fatalf("version import %d", code)
				}
			})
		}
	}
}

func TestSkillBundleErrorsDoNotImport(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		files      map[string]string
		candidates []string
	}{
		{"missing", "skill_root_missing", map[string]string{"download/README.md": "No skill"}, nil},
		{"multiple", "multiple_skills", map[string]string{
			"download/skills/one/SKILL.md": "# One",
			"download/skills/two/SKILL.md": "# Two",
		}, []string{"download/skills/one", "download/skills/two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := login(t)
			body, ct := bundleUpload(t, "zip", tc.files)
			rr := c.do(http.MethodPost, "/api/skills/upload", ct, body)
			var out struct {
				Code       string   `json:"code"`
				Candidates []string `json:"candidates"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != 400 || out.Code != tc.code || !reflect.DeepEqual(out.Candidates, tc.candidates) {
				t.Fatalf("error response %d %s: %v", rr.Code, rr.Body.String(), err)
			}
			if stages, err := os.ReadDir(c.s.stagingDir()); err != nil || len(stages) != 0 {
				t.Fatalf("rejected stage not cleaned: %v %v", stages, err)
			}
			if skills, err := c.s.deps.Skills.List(context.Background()); err != nil || len(skills) != 0 {
				t.Fatalf("rejected bundle created skills: %v %v", skills, err)
			}
		})
	}
}
