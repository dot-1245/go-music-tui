# go-music-tui
a beautiful tui music viewer with playerctl write in Go

> [!NOTE]
> This project is Fully Vibe-coding with Gemini/gh copilot

## Todo
- [x] Live Lyrics from lrclib
- [x] kitty album art
- [ ] Customable KeyConfig
- [ ] Fully Matugen Support
## how to use?

### Before running the project, make sure you have the following requirements installed:
- **playerctl**
- **Go(1.26+ Recommended)**
- **Terminal supported icat(kitty recomended)**
- matugen(advanced)
- **any MPRIS supported Player(Recommend mpv+mpv-mpris or spotify, Firefox and Firefox-Fork sucks at providing artwork)**
- **any Nerd Fonts**

### Step 1: clone this repo 

using https
``` bash
git clone https://github.com/dot-1245/go-music-tui.git
```

using ssh
``` bash
git clone git@github.com:dot-1245/go-music-tui.git
```

**using github CLI(recomended)**
```bash
gh repo clone dot-1245/go-music-tui
```

### Step 2: Enter into repo folder and Build
```bash
cd go-music-tui
go build
./go-music-tui
```
or simply just try it without building
```bash
cd go-music-tui
go run main.go
```
> [!NOTE]
> if you want to send it to other folder, use `-o` options in build

### Step 3: Have fun!

## Advanced: How To use Matugen on this project?
This TUI Reads ~/matugen-colors.txt and use this four Color. so you can use matugen or any software to change color to anything you want(wallpaper, artwork, anything!)

```
primary #ffffff
source_color #4285f4
on_error_container #ffdad6
outline #919191
```

> [!WARNING]
> it's based on my system, it's may cause bug / mess up your nice Rice by wrong path. Be Carefull With ALL Step! Please Read and Verify All commands Before Run!

First, Move to Matugen templates folder (in my system, it's on ~/.config/matugen/templates )
```bash
cd ~/.config/matugen/templates
```

Next, Copy Template File from Repo
For Example:
```bash
curl -O https://raw.githubusercontent.com/dot-1245/go-music-tui/main/colors.txt
```
(make sure you're on templates folder)

Next, Edit matugen config files (in my system, it's on ~/.config/matugen/config.toml)
```bash
cd ..
cat << 'EOF' >> ~/.config/matugen/config.toml

[templates.colors_cache]
input_path = '~/.config/matugen/templates/colors.txt'
output_path = '~/matugen-colors.txt'
EOF
```

Done!
