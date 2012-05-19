// Package pkgwatcher allows for watching for changes in packages or
// their dependencies.
package pkgwatcher

import (
	"fmt"
	"github.com/howeyc/fsnotify"
	"go/build"
	"os"
	"path/filepath"
)

// File level changes including the package that contains it.
type Event struct {
	*fsnotify.FileEvent
	Package *build.Package
}

// A Watcher exposes events via channels notifying on changes in
// monitored packages.
type Watcher struct {
	Packages           map[string]*build.Package // indexed by pkg.ImportPath
	DirPackages        map[string]*build.Package // indexed by pkg.Dir
	Event              chan *Event
	Error              chan error
	workingDirectory   string
	watchedDirectories map[string]bool
	fsnotify           *fsnotify.Watcher
	done               chan bool
}

// Create a new Watcher that monitors all the given import paths. If a
// working directory is not specified, the current working directory
// will be used.
func NewWatcher(importPaths []string, wd string) (w *Watcher, err error) {
	if wd == "" {
		wd, err = os.Getwd()
		if err != nil {
			wd = "/"
		}
	}
	w = &Watcher{
		workingDirectory:   wd,
		Packages:           make(map[string]*build.Package),
		DirPackages:        make(map[string]*build.Package),
		watchedDirectories: make(map[string]bool),
		Event:              make(chan *Event),
	}
	w.fsnotify, err = fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w.Error = w.fsnotify.Error
	go w.proxyEvent()
	go func() {
		for _, p := range importPaths {
			w.WatchImportPath(p)
		}
	}()
	return w, nil
}

// Watch import paths.
func (w *Watcher) WatchImportPath(importPath string) {
	if importPath == "C" {
		return
	}
	if w.Packages[importPath] != nil {
		return
	}
	pkg, err := build.Import(importPath, w.workingDirectory, build.AllowBinary)
	if err != nil {
		w.Error <- fmt.Errorf(
			"Failed to find import path %s with error %s", importPath, err)
		return
	}
	w.Packages[pkg.ImportPath] = pkg
	w.DirPackages[pkg.Dir] = pkg
	for _, path := range pkg.Imports {
		w.WatchImportPath(path)
	}
	for _, pkg := range w.Packages {
		w.WatchDirectory(pkg.Dir)
	}
}

// Watch a directory including it's subdirectories.
func (w *Watcher) WatchDirectory(dir string) {
	if w.watchedDirectories[dir] {
		return
	}

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			w.Error <- fmt.Errorf(
				"Got error when walking directory %s with entry %s and error %s",
				dir, path, err)
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		// TODO remove this surprise
		if filepath.Base(info.Name())[0] == '.' {
			return filepath.SkipDir
		}
		if w.watchedDirectories[path] {
			return nil
		}
		err = w.fsnotify.Watch(path)
		if err != nil {
			w.Error <- fmt.Errorf("Error watching %s: %s", path, err)
		}
		w.watchedDirectories[path] = true
		return nil
	})
}

// Close the Watcher.
func (w *Watcher) Close() error {
	w.done <- true
	return w.fsnotify.Close()
}

// Find's the best guess for the container package.
func (w *Watcher) findPackage(file string) (pkg *build.Package) {
	for file != "." && file != "/" {
		pkg = w.DirPackages[file]
		if pkg != nil {
			return pkg
		}
		file = filepath.Dir(file)
	}
	return nil
}

// Proxy messages from underlying watcher augmenting it to include the
// Package the modified file is contained in.
func (w *Watcher) proxyEvent() {
	for {
		select {
		case ev := <-w.fsnotify.Event:
			w.Event <- &Event{FileEvent: ev, Package: w.findPackage(ev.Name)}
		case <-w.done:
			return
		}
	}
}
