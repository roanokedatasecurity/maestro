package main

import (
	"context"
	"fmt"
)

// App holds application state and lifecycle hooks for the Wails runtime.
type App struct {
	ctx context.Context
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{}
}

// OnStartup is called once when the app starts. The context is saved for
// runtime method calls (e.g. wailsruntime.EventsEmit).
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
}

// OnShutdown is called when the app is about to quit.
func (a *App) OnShutdown(ctx context.Context) {}

// OnDomReady is called after the frontend DOM has fully loaded.
func (a *App) OnDomReady(ctx context.Context) {}

// Greet is a placeholder bound method that validates JS↔Go binding generation.
// Will be replaced with real Maestro API calls in a future sprint.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s from Maestro!", name)
}
