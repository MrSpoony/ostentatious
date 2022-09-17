package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

const port = ":8888"
const configFile = "/Users/u80860794/.config/ostentatious/config.json"
const htmlTemplate = `
<!DOCTYPE html>
<html lang="en">
    <head>
        <title>Login Sucessful</title>
        <meta charset="UTF-8">
    </head>
    <body>
        <h1>Login Successful</h1>
        <p>If this page isn't automatically closing you can close it now.</p>
        <script>
            window.close();
        </script>
    </body>
</html>
`

var redirectURI = fmt.Sprintf("http://localhost%s/callback", port)

var (
	auth = spotifyauth.New(spotifyauth.WithRedirectURL(redirectURI), spotifyauth.WithScopes(
		spotifyauth.ScopeUserReadPrivate,
		spotifyauth.ScopePlaylistReadPrivate,
		spotifyauth.ScopePlaylistReadCollaborative,
		spotifyauth.ScopePlaylistModifyPrivate,
		spotifyauth.ScopePlaylistModifyPublic,
		spotifyauth.ScopeUserReadPlaybackState,
	))
	ch    = make(chan *spotify.Client)
	state = "abc123"
)

type Data struct {
	Token        oauth2.Token `json:"token"`
	PlaylistName string       `json:"playlist_name"`
}

// TODO: Improve error handling (dont panic everywhere)
func main() {
	ctx := context.Background()
	var setPlaylist bool
	flag.BoolVar(&setPlaylist, "reset", false, "To reset the playlist chosen by the first startup")
	flag.Parse()
	var data Data
	go func() {
		content, err := os.ReadFile(configFile)
		if err != nil {
			file, errr := os.Create(configFile)
			if errr != nil {
				panic(errr)
			}
			defer file.Close()
			startserver()
			return
		}
		err = json.Unmarshal(content, &data)
		if err != nil {
			startserver()
			return
		}
		client := spotify.New(auth.Client(ctx, &data.Token))
		if client == nil {
			startserver()
			return
		}
		ch <- client
	}()

	// wait for auth to complete
	client := <-ch

	newTok, err := client.Token()
	if err != nil {
		panic(err)
	}
	data.Token = *newTok
	user, err := client.CurrentUser(ctx)
	if err != nil {
		panic(err)
	}
	playlistPager, err := client.GetPlaylistsForUser(ctx, user.ID)
	if err != nil {
		panic(err)
	}
	playlists := getAllPlaylists(ctx, client, playlistPager)
	playlistNames := make([]string, len(playlists))
	for i, p := range playlists {
		playlistNames[i] = p.Name
	}
	if setPlaylist || data.PlaylistName == "" {
		data.PlaylistName = selectPlaylist(playlistNames)
	}
	var playlist spotify.SimplePlaylist
	for _, p := range playlists {
		if p.Name != data.PlaylistName {
			continue
		}
		playlist = p
		break
	}
	currentlyPlaying, err := client.PlayerCurrentlyPlaying(ctx)
	if err != nil {
		panic(err)
	}
	trackID := currentlyPlaying.Item.ID
	playlistTrackPager, err := client.GetPlaylistTracks(ctx, playlist.ID)
	if err != nil {
		panic(err)
	}
	tracks := getPlaylistTracks(ctx, client, playlistTrackPager)
	var isAlreadyInPlaylist bool
	for _, track := range tracks {
		if trackID == track.Track.ID {
			isAlreadyInPlaylist = true
		}
	}
	if isAlreadyInPlaylist {
		writeData(data)
		return
	}
	_, err = client.AddTracksToPlaylist(ctx, playlist.ID, trackID)
	if err != nil {
		panic(err)
	}
	writeData(data)
}

func writeData(data Data) {
	out, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	file, err := os.Create(configFile)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	file.Write(out)
}

func getAllPlaylists(ctx context.Context, client *spotify.Client, p *spotify.SimplePlaylistPage) []spotify.SimplePlaylist {
	playlists := []spotify.SimplePlaylist{}
	for {
		playlists = append(playlists, p.Playlists...)
		if err := client.NextPage(ctx, p); err == spotify.ErrNoMorePages {
			break
		} else if err != nil {
			panic(err)
		}
	}
	return playlists
}

func getPlaylistTracks(ctx context.Context, client *spotify.Client, p *spotify.PlaylistTrackPage) []spotify.PlaylistTrack {
	tracks := []spotify.PlaylistTrack{}
	for {
		tracks = append(tracks, p.Tracks...)
		if err := client.NextPage(ctx, p); err == spotify.ErrNoMorePages {
			break
		} else if err != nil {
			panic(err)
		}
	}
	return tracks
}

func selectPlaylist(playlists []string) string {
	prompt := promptui.Select{
		Label: "Select Playlist",
		Items: playlists,
	}
	prompt.Searcher = func(in string, i int) bool {
		return strings.Contains(
			strings.ToLower(prompt.Items.([]string)[i]),
			strings.ToLower(in),
		)
	}
	prompt.StartInSearchMode = true
	_, res, err := prompt.Run()

	if err != nil {
		panic(err)
	}
	return res
}

func openbrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		panic(err)
	}

}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		panic(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		panic(fmt.Sprintf("State mismatch: %s != %s\n", st, state))
	}

	w.Write([]byte(htmlTemplate))

	// use the token to get an authenticated client
	client := spotify.New(auth.Client(r.Context(), tok))
	ch <- client
}

func startserver() {
	http.HandleFunc("/callback", completeAuth)
	go func() {
		err := http.ListenAndServe(port, nil)
		if err != nil {
			panic(err)
		}
	}()

	url := auth.AuthURL(state)
	openbrowser(url)
}
