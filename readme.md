## jellyrpc

a simple jellyfin discord rpc daemon written in golang

<p align="center">
  <img src="images/movie.png" width="48%" />
  <img src="images/series.png" width="48%" />  
</p>

supports local and public jellyfin instances, and doesn't require any extra api keys for cover art support on local instances

```md
features:
  - lightweight
  - cover art fetching
  - pause state handling
  - efficient socket mgmt
  - systemd user service
```

### install

> requires `go` and `make`

```bash
git clone https://github.com/reckedpr/jellyrpc

cd jellyrpc

make install
```

the makefile install only supports unix like systemd operating systems, daemon can run on non sytemd systems but will require manual service setup (e.g. on openrc, runit, etc.)

should run on macos but untested, no support for windows

#### config

edit the config file at `~/.config/jellyrpc/config` 

*the makefile should create this automatically, or use the `config.example` file in this repo*

set/edit the following values:

- `JELLYFIN_URL` with your jellyfin instance, ensuring you include the protocol
- `JELLYFIN_KEY` with an api key generated under dashboard > api keys
- `JELLYFIN_USER` with the jellyfin username of who you want to use the status of

now you can run `systemctl --user enable --now jellyrpc` to start the daemon

if you have any problems run `journalctl --user -u jellyrpc -n 30` and/or make an issue

#### optional settings

- `POLL_RATE` can be set to an integer(>0) to set how often the daemon will poll in seconds

#### manual install

installing manually depends on how you plan to run the daemon as a service, but you can get running by:

```bash
git clone https://github.com/reckedpr/jellyrpc

cd jellyrpc

go build -ldflags="-s -w"

# copy the example config
mkdir -p ~/.config/jellyrpc

cp ./config.example ~/.config/jellyrpc/config

# run the binary
./jellyrpc

```

### update

to update run

```bash
git pull && make install
```

or if installed manually then `git pull` and rebuild as needed
