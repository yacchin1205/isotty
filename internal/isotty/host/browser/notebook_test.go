package browser

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pixel = []byte("\x89PNG\r\n\x1a\nfake")

func notebookSource() string {
	png := base64.StdEncoding.EncodeToString(pixel)
	return `{
 "nbformat": 4, "nbformat_minor": 5, "metadata": {},
 "cells": [
  {"cell_type": "markdown", "id": "intro", "metadata": {}, "source": ["# Sales\n", "\n", "![pasted](attachment:shot.png) <b>raw</b>"],
   "attachments": {"shot.png": {"image/png": "` + png + `"}}},
  {"cell_type": "code", "id": "load", "metadata": {}, "execution_count": 3, "source": "df = load()\ndf.head()",
   "outputs": [
    {"output_type": "stream", "name": "stdout", "text": ["loading <1>\n", "50%\r100%\n"]},
    {"output_type": "execute_result", "execution_count": 3, "metadata": {}, "data": {"text/html": "<table><tr><td>1</td></tr></table><script>fetch('/')</script>", "text/plain": "   a\n0  1"}},
    {"output_type": "display_data", "metadata": {}, "data": {"image/png": "` + png + `\n", "text/plain": "<Figure>"}},
    {"output_type": "display_data", "metadata": {}, "data": {"application/vnd.jupyter.widget-view+json": {"model_id": "x"}}},
    {"output_type": "error", "ename": "ValueError", "evalue": "bad", "traceback": ["\u001b[0;31mValueError\u001b[0m: bad"]}
   ]}
 ]
}`
}

// A notebook is shown as its cells with what they printed and drew: the
// Markdown converted, the code line by line, text outputs as text, and an
// image or an HTML output served on its own, the HTML in a frame that runs
// no script.
func TestViewerShowsANotebookWithItsOutputs(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sales.ipynb"), []byte(notebookSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })
	resp := get(t, files.url("sales.ipynb"), "")
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-src 'self'") {
		t.Fatalf("served %d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Security-Policy"), page)
	}
	out := "/" + files.token + "/out/sales.ipynb"
	for _, want := range []string{
		`data-kind="notebook"`,
		`<section class="cell markdown" data-cell="1" data-cell-id="intro"><div class="label">[1] markdown</div>`,
		`<div class="block" data-lines="1-1"><h1>Sales</h1>`,
		`<img src="` + out + `?cell=1&amp;attachment=shot.png" alt="pasted">`,
		`<section class="cell code" data-cell="2" data-cell-id="load"><div class="label">[2] code · In [3]</div>`,
		`<tr><td class="n" data-line="2">2</td><td class="c">df.head()</td></tr>`,
		`<div class="output" data-output="1"><div class="label">output 1</div><pre class="stream stdout">loading &lt;1&gt;` + "\n" + `100%</pre>`,
		`<iframe class="html" sandbox="allow-same-origin" src="` + out + `?cell=2&amp;output=2"></iframe>`,
		`<img src="` + out + `?cell=2&amp;output=3" alt="">`,
		`This output (application/vnd.jupyter.widget-view+json) is not shown here.`,
		`<pre class="error">ValueError: bad</pre>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %s:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<b>raw</b>") || strings.Contains(page, "<script>fetch") || strings.Contains(page, `id="L`) {
		t.Fatalf("the notebook's HTML reached the page, or rows of cells share ids:\n%s", page)
	}

	served := func(query string) *http.Response {
		return get(t, "http://"+files.listener.Addr().String()+out+"?"+query, "")
	}
	for _, query := range []string{"cell=2&output=3", "cell=1&attachment=shot.png"} {
		resp := served(query)
		got, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || string(got) != string(pixel) || !strings.HasPrefix(resp.Header.Get("Content-Security-Policy"), "sandbox;") {
			t.Fatalf("%s: %d %s %s %q", query, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Security-Policy"), got)
		}
	}
	resp = served("cell=2&output=2")
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
		resp.Header.Get("Content-Security-Policy") != notebookOutputPolicy || !strings.Contains(string(got), "<table><tr><td>1</td></tr></table>") {
		t.Fatalf("html output: %d %s %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Security-Policy"), got)
	}
	if !strings.HasPrefix(notebookOutputPolicy, "sandbox allow-same-origin;") || strings.Contains(notebookOutputPolicy, "allow-scripts") || strings.Contains(notebookOutputPolicy, "script-src") {
		t.Fatalf("an HTML output may run a script: %s", notebookOutputPolicy)
	}
	// What is put on the page is not served, nor is what is not there.
	for _, query := range []string{"cell=2&output=1", "cell=2&output=4", "cell=2&output=9", "cell=3&output=1", "cell=0&output=1", "cell=1&attachment=none.png", "cell=x"} {
		if resp := served(query); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", query, resp.StatusCode)
		}
	}
	if resp := get(t, "http://"+files.listener.Addr().String()+out+"?cell=2&output=3", "localhost"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another host name reached an output: %d", resp.StatusCode)
	}
}

func TestOutputsAreServedOnlyFromNotebooks(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sales.json"), []byte(notebookSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "old.ipynb"), []byte(`{"nbformat": 3, "worksheets": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })
	if resp := get(t, "http://"+files.listener.Addr().String()+"/"+files.token+"/out/sales.json?cell=2&output=3", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an output of a file that is not a notebook: %d", resp.StatusCode)
	}
	resp := get(t, files.url("old.ipynb"), "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "nbformat 3") {
		t.Fatalf("an nbformat 3 notebook: %d %s", resp.StatusCode, body)
	}
}

// A comment on a notebook names the cell, by position and id, and in it
// lines of the source or one output.
func TestReviewsOnANotebookNameTheCell(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sales.ipynb"), []byte(notebookSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })
	for _, anchor := range []string{
		`{"cell":2,"cell_id":"load","lines":[1,2],"quote":"df = load()"}`,
		`{"cell":2,"cell_id":"load","output":2}`,
		`{"cell":1}`,
	} {
		if status, answer := review(t, files, http.MethodPost, "sales.ipynb", `{"anchor":`+anchor+`,"body":"x"}`); status != http.StatusOK || !strings.Contains(answer, strings.TrimSuffix(anchor[1:], "}")) {
			t.Fatalf("%s: %d %s", anchor, status, answer)
		}
	}
	for _, anchor := range []string{
		`{"cell":0}`,
		`{"cell_id":"load"}`,
		`{"output":1}`,
		`{"cell":2,"output":0}`,
		`{"cell":2,"output":1,"lines":[1,1]}`,
		`{"cell":2,"quote":"x"}`,
		`{"cell":2,"point":[0.5,0.5]}`,
	} {
		if status, answer := review(t, files, http.MethodPost, "sales.ipynb", `{"anchor":`+anchor+`,"body":"x"}`); status != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", anchor, status, answer)
		}
	}
}
