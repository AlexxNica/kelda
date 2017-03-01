package inspect

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"

	"github.com/quilt/quilt/stitch"
	"github.com/quilt/quilt/util"
)

func TestSlug(t *testing.T) {
	test := map[string]string{
		"slug.spec":       "slug",
		"a/b/c/slug.spec": "a/b/c/slug",
		"foo":             "err",
	}

	for inp, expect := range test {
		if sl, err := getSlug(inp); err != nil {
			if expect != "err" {
				t.Error(err)
			}
		} else if sl != expect {
			t.Error(sl)
		}
	}
}

func initSpec(src string) (stitch.Stitch, error) {
	return stitch.FromJavascript(src, stitch.ImportGetter{
		Path: "../specs",
	})
}

const testStitch = `var a = new Service("a", [new Container("ubuntu")]);
	var b = new Service("b", [new Container("ubuntu")]);
	var c = new Service("c", [new Container("ubuntu")]);

	deployment.deploy([a, b, c]);

	a.connect(22, b);
	b.connect(22, c);`

// The expected graphviz graph returned by inspect when run on `testStitch`.
const expGraph = `strict digraph {
    subgraph cluster_0 {
        33595ff0350bdd0e78b1fefb3f5b666ea661a5b8;
        6658237d394668acaef0d3211949e6cefcbd7846;
        99301a9ea4b177ba36eba250cc3c55d990dc6dba;
        public;
    }
    33595ff0350bdd0e78b1fefb3f5b666ea661a5b8 -> 99301a9ea4b177ba36eba250cc3c55d990dc6dba
    99301a9ea4b177ba36eba250cc3c55d990dc6dba -> 6658237d394668acaef0d3211949e6cefcbd7846
}`

func isGraphEqual(a, b string) bool {
	a = strings.Replace(a, "\n", "", -1)
	a = strings.Replace(a, " ", "", -1)
	b = strings.Replace(b, "\n", "", -1)
	b = strings.Replace(b, " ", "", -1)
	return a == b
}

func TestViz(t *testing.T) {
	t.Parallel()

	spec, err := initSpec(testStitch)
	if err != nil {
		panic(err)
	}

	graph, err := stitch.InitializeGraph(spec)
	if err != nil {
		panic(err)
	}

	gv := makeGraphviz(graph)
	if !isGraphEqual(gv, expGraph) {
		t.Error(gv + "\n" + expGraph)
	}
}

func TestMain(t *testing.T) {
	util.AppFs = afero.NewMemMapFs()
	util.WriteFile("test.js", []byte(testStitch), 0644)

	exitCode := Main([]string{"test.js", "graphviz"})

	assert.Zero(t, exitCode)
	res, err := util.ReadFile("test.dot")
	assert.Nil(t, err)
	assert.True(t, isGraphEqual(expGraph, res))
}

func TestMainArgErr(t *testing.T) {
	t.Parallel()

	exitCode := Main([]string{"test.js"})
	assert.NotZero(t, exitCode)
}
