# hammock-shell's interactive start: what a login shell reads, from the
# Hammock home (see hammock-shell).
. /usr/local/hammock/etc/profile
if [ -r "$HOME/.bash_profile" ]; then . "$HOME/.bash_profile"
elif [ -r "$HOME/.bash_login" ]; then . "$HOME/.bash_login"
elif [ -r "$HOME/.profile" ]; then . "$HOME/.profile"; fi
