# Records docs/demo.gif with VHS (https://github.com/charmbracelet/vhs).
# Run from the repository root:
#
#   vhs docs/demo/demo.tape
#
# It builds ghwatch and the demo daemon (docs/demo/main.go) into a scratch
# directory and plays a scripted story, so it needs no GitHub access and
# does not touch your watchlist or a running daemon.
#
# The alert bell is a Nerd Fonts glyph: set FontFamily to a Nerd Font you
# have installed, or it shows as a box.

Output docs/demo.gif

Require go

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font"
Set FontSize 15
Set Width 1100
Set Height 560
Set Padding 12
Set Theme "Catppuccin Mocha"
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

# Story time in brackets: the scripted events happen at 9s, 12s, 20s, 30s.

# #123: jobs are running; turn its alerts on. [~2s]
Type "ghwatch" Sleep 400ms Enter
Sleep 2.5s
Type "j" Sleep 600ms
Type "j" Sleep 600ms
Type "n" Sleep 2s

# e2e-aws-ovn fails and moves to the Failed group. [9s]
Sleep 3s

# #131 is in the merge queue; enter opens the queue page. [12s]
Type "l" Sleep 2.5s
Enter Sleep 2.5s

# Notification settings.
Type "N" Sleep 2.5s
Escape

# #131 moves up the queue [20s], then merges [30s].
Sleep 11.5s

Type "?" Sleep 3s
Type "x" Sleep 1s
Type "q" Sleep 500ms

Hide
Type "pkill -f $D/demo; rm -rf $D" Enter
