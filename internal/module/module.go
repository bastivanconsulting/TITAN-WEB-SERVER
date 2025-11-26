package module

import (
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/dop251/goja"
)

// JSModule represents a compiled JavaScript module
type JSModule struct {
	runtime *goja.Runtime
	program *goja.Program
}

// New creates a new JSModule from a script file
func New(scriptPath string) (*JSModule, error) {
	script, err := ioutil.ReadFile(scriptPath)
	if err != nil {
		return nil, err
	}

	program, err := goja.Compile(scriptPath, string(script), true)
	if err != nil {
		return nil, err
	}

	return &JSModule{
		runtime: goja.New(),
		program: program,
	}, nil
}

// Run executes the module with the given request and response objects
func (m *JSModule) Run(r *http.Request, w http.ResponseWriter) error {
	_, err := m.runtime.RunProgram(m.program)
	if err != nil {
		return err
	}

	main, ok := goja.AssertFunction(m.runtime.Get("main"))
	if !ok {
		return errors.New("main function not found in module")
	}

	// Create request and response objects to pass to the JS function
	reqObj := m.runtime.NewObject()
	reqObj.Set("method", r.Method)
	reqObj.Set("url", r.URL.String())
	reqObj.Set("headers", r.Header)

	resObj := m.runtime.NewObject()
	resObj.Set("headers", w.Header())

	_, err = main(goja.Undefined(), reqObj, resObj)
	return err
}
