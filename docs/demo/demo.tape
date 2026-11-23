# Records docs/demo.gif with VHS (https://github.com/charmbracelet/vhs).
# Run from the repository root:
#
#   vhs docs/demo/demo.tape
#
# It builds ghwatch and the demo daemon (docs/demo/main.go) into a scratch
# directory and plays a scripted story, so it needs no GitHub access and
# does not touch your watchlist or a running daemon.
#
# The alert bell and the comment mark are Nerd Fonts glyphs (codicons):
# set FontFamily to a Nerd Font you have installed, or they show as boxes.

Output docs/demo.gif

Require go

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font"
Set FontSize 15
Set Width 1100
Set Height 640
Set Padding 12
Set Theme "Catppuccin Frappe"
Set TypingSpeed 80ms
Set CursorBlink false

Hide
Type "export PS1='$ ' D=$(mktemp -d)" Enter
Type "export XDG_CONFIG_HOME=$D/c XDG_STATE_HOME=$D/s XDG_RUNTIME_DIR=$D/r BROWSER=true PATH=$D:$PATH" Enter
Type "go build -o $D/ghwatch ./cmd/ghwatch && go build -o $D/demo ./docs/demo && echo build-$((1+1))-ok" Enter
Wait+Screen /build-2-ok/
# The story clock starts with the demo daemon: keep the next steps quick.
Type "(demo >/dev/null 2>&1 &); sleep 0.5; clear" Enter
Sleep 700ms
Show

# Story time in brackets: the scripted events happen at 5s (a new
# comment), 13s (a job fails), 16s, 25s and 34s (the merge queue).

# #123 opens on its comments; a reviewer replies in a code thread. [~1s]
Type "ghwatch" Sleep 400ms Enter
Sleep 5.5s

# Expand the thread and read it, down to the new comment. [~7s]
Type "k" Sleep 700ms
Type "e" Sleep 1.5s
Type "j" Sleep 500ms
Type "j" Sleep 500ms
Type "j" Sleep 2s

# The checks: e2e-aws-ovn fails and moves to the Failed group. [~12s]
Type "v" Sleep 3.5s
# Alerts on for the PR.
Type "n" Sleep 2s

# #131 is in the merge queue; enter opens the queue page. [~18s]
Type "l" Sleep 2.5s
Enter Sleep 2s

# Notification settings.
Type "N" Sleep 2.5s
Escape Sleep 3.5s

# Keys, then the legend. [~29s]
Type "?" Sleep 2.5s
Tab Sleep 3.5s
Type "x" Sleep 1s

# #131 moved up the queue [25s], and merges [34s].
Sleep 4s
Type "q" Sleep 500ms

Hide
Type "pkill -f $D/demo; rm -rf $D" Enter
