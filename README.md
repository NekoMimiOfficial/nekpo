# Nekpo  
Neko's repo tools, with this amazing tool the sky is the limit when it comes to using Neko's projects  
You can build, install, init apps or manage build systems and dependencies plus run a CI tool for Neko Store staff  
For the general public the main use of this would be to build C++ apps or install them from git repos or contributing to any Neko app that is hosted on the Neko Store or requires the NekoEco SDK that allows access to the Ecosystem utilities, to obtain developer docs and SDKs please contact Neko directly at [mail](mailto:nekomimi@tilde.team)  

# Building  
This app is built for Linux  
clone this project and make sure you have a `golang` toolchain installed then run `cd src && go build -o nekpo ./cmd/nekpo` which will generate the `nekpo` multi-tool  

# Config  
Nekpo's config lives in `$HOME/.config/nekpo/builder.conf`  
Example config:  
```toml
server = https://nekostore.nekos.team
key_file = /home/nekomimi/.local/share/nekpo/builder.key
work_dir = /home/nekomimi/.local/share/nekpo/data
poll_seconds = 300

[repo]
name = NekoDrome
url = https://github.com/NekoMimiOfficial/NekoDrome.git
branch = main
```
You most likely wont need to use `key_file` since that's a Neko Store staff key used to access the store APIs to publish and manage apps and dependencies  

# Usage  
Most things are well documented in the `--help` arg, what shouldn't be used by the public isn't very documented so don't worry about using it  
- `nekpo init .`: generates project root for your app and integrates it with nekpo, it's interactive and will tell you everything you need to use it  
- `nekpo build .`: builds the current project based on your build script/system  
- `nekpo release major|minor|patch`: bumps the corresponding version number and writes to the changelog.md file if configured, also pulls commits from git to include in the changelog  
- `nekpo install <gh+AUTHOR/REPO | git-url | store:APP-ID>`: builds and installs an app locally from github or a git provider or installs the prebuilt packages from the store  
- `nekpo uninstall <app>`: nukes the local app you installed via nekpo  
- `nekpo list`: lists all installed nekpo apps and their version and install date  
- `nekpo outdated`: tells you what apps are outdated and ready to update  
- `nekpo update`: updates all packages or a specified package if appeneded to the command  
- `nekpo doctor`: checks the nekpo config in the current project and tells you if any issues exist  
- `nekpo completion bash|zsh|fish`: prints out a completion script for you to install to your shell  
- `nekpo version`: does stuff  

anything not mentioned here is not supposed to be used by the public, if you wanna use the dependency manager contact Neko for support and for her to add any dependency you need, we dont open this to the public because we dont have a repository of dependencies to be used by the public, this is mainly used to build our projects on any distro  

# Neko's repo  
All information about Neko and her repo and all the tools and apps and the story of her life can be found [here](https://github.com/NekoMimiOfficial)
