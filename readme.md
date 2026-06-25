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

> requires `git`, `go` and `make`

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

#### using user token instead of api key
<details>
  <summary>
expand me
  </summary>

if you don't have access to api keys on your jellyfin instance (e.g. using a friends instance) you can still use your user token for rpc

you can fetch this manually by grabbing the token from the `Authorization` header in a request and looking at the `Token=""` value

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
    console.log("click literally anything and it should output ur token");
})();
```

you can then just use this token as value for the `JELLYFIN_KEY` option in config

</details>

#### optional settings

- `POLL_RATE` can be set to an integer(>0) to set how often the daemon will poll in seconds
- `APP_ID` can be set to override the default discord application id with your own
- `DB_LINK` can be set to `true` to enable a link on the rpc activity to imdb/tvdb
- `USE_EPISODE_ART` can be set to `true` to prefer using episode specific cover art

#### manual install

installing manually depends on how you plan to run the daemon as a service, but you can get running by:

```bash
git clone https://github.com/reckedpr/jellyrpc

cd jellyrpc

go build -ldflags="-s -w"

# OR optionally build with the git commit hash embedded
go build -ldflags="-s -w -X main.gitHash=$(git rev-parse --short HEAD)"

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
