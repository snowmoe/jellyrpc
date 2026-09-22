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
  - idle pause timeout
  - efficient socket mgmt
  - systemd user service
```

> local cover art is served by a bridge service (rot.sh) using the media id, it logs nothing and only discord's servers fetch the image

## install

### arch (aur)

```bash
yay -S jellyrpc
```
*replace `yay` with your preferred aur helper (e.g. paru)*

### other

> requires `git`, `go` and `make`

```bash
git clone https://github.com/snowmoe/jellyrpc

cd jellyrpc

make install
```

> the makefile install action only supports unix like operating systems using systemd, non systemd systems will require manual service creation for your init system (e.g. openrc, runit, etc.)

## setup

create and/or edit the config file at `~/.config/jellyrpc/config`, an example config can be found [here](https://github.com/snowmoe/jellyrpc/blob/main/config.example)

> e.g.
>```bash
>mkdir -p ~/.config/jellyrpc
>touch ~/.config/jellyrpc/config
>```

*if you installed from the aur an example file also exists at `/usr/share/doc/jellyrpc/config.example`*

#### set the following (required) values:

- `JELLYFIN_URL` with your jellyfin instance, ensuring you include the protocol
- `JELLYFIN_KEY` with an api key generated under **dashboard > api keys** || see below to use without an api key
- `JELLYFIN_USER` with the jellyfin username of who you want to use the status of

now you can run `systemctl --user enable --now jellyrpc` to start the daemon

if you have any problems run `journalctl --user -u jellyrpc -n 30` and/or make an issue

<details>
  <summary>
<strong>how to use without an api key</strong>
  </summary>
<br>
if you don't have access to api keys on your jellyfin instance (e.g. using a friends instance) you can still use your user token for rpc.

you can fetch this manually by looking at a requests `Authorization` header in devtools, and grabbing at the `Token=""` value

alternatively you can use the below js snippet by pressing F12 and pasting it in the console:

```js
(function() {
    const originalFetch = window.fetch;
    window.fetch = async function(...args) {
        const headers = args[1]?.headers || {};
        const token = headers['X-MediaBrowser-Token'] || headers['Authorization'] || headers['X-Emby-Token'];

        if (token) {
            console.log("jellyfin token:", token.includes('Token=') ? token.split('Token="')[1].split('"')[0] : token);
            window.fetch = originalFetch;
        }
        return originalFetch.apply(this, args);
    };
    console.log("click anything and it should output your token");
})();
```

you can then just use this token for the `JELLYFIN_KEY` option in config
</details>

### full config options

|option|default|meaning|
|-|:-:|-|
|*`JELLYFIN_URL`|  | jellyfin instance to use
|*`JELLYFIN_KEY`|  | jellyfin api key to authenticate api requests
|*`JELLYFIN_USER`|  | jellyfin user(name) to get the status of
|`POLL_RATE`| 5 | how often the daemon will poll in seconds
|`PAUSE_TIMEOUT`| 10 | number of minutes idle before stopping rpc (0 = disabled)
|`APP_ID`| [main.go:12](https://github.com/snowmoe/jellyrpc/blob/701e2ea3de536bb47bf0dcf663f1c45ca4cb23c3/main.go#L12) | discord app id to use for rpc
|`DB_LINK`| false | use rpc title as a link to imdb/tvdb for the episode
|`USE_EPISODE_ART`| false | prefer using per episode cover art (for series')

*required

## manual build

you can build the binary with `make build`, or manually:

```bash
git clone https://github.com/snowmoe/jellyrpc

cd jellyrpc

go build -ldflags="-s -w"

# or if you want to build with the version tag embedded
go build -ldflags="-s -w -X main.gitVersion=$(git describe --tags --abbrev=0)"
```

then configure as normal, and run however you'd like

## update

aur package updates automatically (duh)

otherwise run `git pull` in the cloned repo, then `make install` again or rebuild manually

finally restart the service e.g. `systemctl --user restart jellyrpc`
