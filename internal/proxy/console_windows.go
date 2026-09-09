package proxy

import "golang.org/x/sys/windows"

// HideConsole hides the console window this process was given, if it has one.
//
// A logon task running a console binary shows a window. Left alone that is a black
// box appearing at every login, or sitting on the desktop for the life of the
// session, over an agent whose whole job is to be unobtrusive.
//
// `-H windowsgui` is the usual answer and it is the wrong one here: neverseen.exe is
// a CLI, and a GUI-subsystem build writes nothing to the console it was run from, so
// `neverseen status` would print nothing into the terminal somebody typed it in. A
// second windowless binary is worse still — it would assemble the pipeline, which is
// the bar CLAUDE.md sets for a new entrypoint and it would not clear it.
//
// So the console is hidden at run time, and only when the caller asks: the task
// passes --detach and a person does not.
//
// NewLazySystemDLL rather than syscall.NewLazyDLL: the latter uses the default search
// order, which includes the directory the executable was started from. Both of these
// are KnownDLLs so the exposure is small, but there is no reason to take it, and
// golang.org/x/sys is already a direct dependency for internal/secure.
func HideConsole() {
	getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	showWindow := windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow")

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		// No console at all, which is what a task configured to run whether the user
		// is logged on or not gives. Nothing to hide.
		return
	}
	const swHide = 0
	_, _, _ = showWindow.Call(hwnd, swHide)
}
