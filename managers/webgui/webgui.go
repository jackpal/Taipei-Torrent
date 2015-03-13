package webgui

import (
	"code.google.com/p/gowut/gwu"
	"errors"
	"fmt"
	"github.com/jackpal/Taipei-Torrent/torrent"
	"strconv"
	"time"
)

type WebGui struct {
	//Why double-pointer? Because methods defined by an interface have to act on values
	//This makes self-modification difficult
	torrentCtrl **torrent.TorrentControl
	WebPort     int
}

func (wg WebGui) Start(tc *torrent.TorrentControl) error {
	(*wg.torrentCtrl) = tc
	if tc.TorrentSessions != nil {
		go wg.start(wg.WebPort)
		return nil
	} else {
		return errors.New("Didn't get a valid TorrentControl when starting.")
	}
}

func (wg *WebGui) start(port int) {
	// Create and build a window
	win := gwu.NewWindow("main", "Status Info")
	win.Style().SetFullWidth()
	win.SetHAlign(gwu.HA_CENTER)
	win.SetCellPadding(2)

	//UpdateTime label
	updateTime := gwu.NewLabel("")
	win.Add(updateTime)

	//Panel for torrent labels
	torrPanel := gwu.NewVerticalPanel()
	win.Add(torrPanel)

	//torrent label holder
	torrentLabels := make([]gwu.Label, 0, 16)

	//Timer set for every second
	t2 := gwu.NewTimer(time.Second)
	t2.SetRepeat(true)
	t2.AddEHandlerFunc(func(e gwu.Event) {
		updateTime.SetText(fmt.Sprintln(time.Now().Format("2006-01-02 15:04:05")))
		e.MarkDirty(updateTime)

		torrlist := (**wg.torrentCtrl).GetTorrentList()
		for i := len(torrentLabels); i < len(torrlist); i++ {
			newLabel := gwu.NewLabel("pants")
			torrentLabels = append(torrentLabels, newLabel)
			torrPanel.Insert(newLabel, 0)
			e.MarkDirty(torrPanel)
		}

		for i, labl := range torrentLabels {
			if i < len(torrlist) {
				mpa, _ := (**wg.torrentCtrl).GetStatus(torrlist[i])
				labl.SetText(mpa["Name"] + "  At  " + mpa["Percent"] + "%")
			} else {
				labl.SetText("")
			}
			e.MarkDirty(labl)
		}
	}, gwu.ETYPE_STATE_CHANGE)
	win.Add(t2)

	server := gwu.NewServer("TaipeiTorrent", "localhost:"+strconv.Itoa(port))
	server.SetText("TaipeiTorrent WebGui")
	server.AddWin(win)
	server.Start("main")
}
