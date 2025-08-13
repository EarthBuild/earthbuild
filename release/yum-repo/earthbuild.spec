Summary: Build automation tool for the container era
Name: earthbuild
Version: __earthbuild_version__
Release: 1
License: Business Source License
URL: https://earthbuild.dev
Group: System
Packager: earthbuild team
Requires: bash
BuildRoot: /work/rpmbuild/

%description
Build automation tool for the container era

%install
mkdir -p %{buildroot}/usr/bin/
cp /usr/local/bin/earthbuild %{buildroot}/usr/bin/earthbuild

%files
/usr/bin/earthbuild

%post
set -e
# install bash auto completion
BASH_COMPLETION_DIR="/usr/share/bash-completion/completions"
if [ -d "$BASH_COMPLETION_DIR" ]
then
    earthbuild bootstrap --source bash > "$BASH_COMPLETION_DIR/earthbuild"
fi

# install zsh auto completion
ZSH_COMPLETION_DIR="/usr/local/share/zsh/site-functions"
if [ -d "$ZSH_COMPLETION_DIR" ]
then
    earthbuild bootstrap --source zsh > "$ZSH_COMPLETION_DIR/_earthbuild"
fi

frontend="${frontend:-$(which docker || which podman || true)}"
if [ -z "$frontend" ]; then
    echo "neither docker nor podman was found; skipping earthbuild bootstrap"
    exit
fi

# skip bootstrapping if docker isn't installed or running
if ! "$frontend" info 2>/dev/null >/dev/null
then
    echo "unable to query docker/podman daemon; skipping earthbuild bootstrap"
    exit
fi

echo "bootstrapping earthbuild"
earthbuild bootstrap
echo "bootstrapping earthbuild done"

%postun
set -e

if [ "$1" -eq 0 ]; then
  # "$1" is set to the number of packages left after operation; should be 1 on upgrade, 0 on uninstall.
  UNABLE_TO_REMOVE="unable to remove earthbuild-related docker resources"

  rm -f /usr/share/bash-completion/completions/earthbuild
  rm -f /usr/local/share/zsh/site-functions/_earthbuild

  frontend="${frontend:-$(which docker || which podman || true)}"
  if [ -z "$frontend" ]; then
      echo "neither docker nor podman was found; $UNABLE_TO_REMOVE"
      exit
  fi

  if ! "$frontend" info 2>/dev/null >/dev/null
  then
      echo "unable to query docker/podman daemon; $UNABLE_TO_REMOVE"
      exit
  fi

  echo "removing earthbuild-buildkitd docker/podman container"
  "$frontend" rm --force earthbuild-buildkitd

  echo "removing earthbuild-cache docker/podman volume"
  "$frontend" volume rm --force earthbuild-cache
fi

%changelog
* Thu Feb 25 2021 alex <alex@earthbuild.dev>
- initial poc
