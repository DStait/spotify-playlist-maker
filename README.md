# Spotify playlist maker

> [!WARNING]
>  First attempt at Go, expect bugs

## Setup

### Spotify application
Need to first create application at spotify dev portal. Intructions can be found at the following link. 

https://developer.spotify.com/documentation/web-api

The `redirect uri` should be set to `http://localhost:8080/callback`

### Setup the env file
Copy the `env.example` and set your vars. The `REFRESH_TOKEN` can be ignored until you complete auth. 


## Obtaining the refresh token
Run first time with `--auth` to get refresh token. 
```bash
docker run --rm --env-file ./env -p 8080:8080 dstait/spotify-playlist-maker:latest --auth
```
or to a file with the following command. Make sure to mount a volume for the config file to be written to.
```bash
docker run --rm --env-file ./env -v ./config:/conf -p 8080:8080 dstait/spotify-playlist-maker:latest --auth --file
```


## Running
With the refresh token obtained you can now run the app. 
```bash
docker run --rm --env-file ./env -p 8080:8080 dstait/spotify-playlist-maker:latest

```