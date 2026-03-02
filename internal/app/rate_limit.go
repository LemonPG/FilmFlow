package app

import (
	"context"

	"github.com/LemonPG/115driver/pkg/driver"
)

// waitForRateLimit waits for the rate limiter if configured
func (a *App) waitForRateLimit(ctx context.Context) error {
	if a.limiter == nil {
		return nil
	}
	return a.limiter.Wait(ctx)
}

// rateLimitedList wraps c.List with rate limiting
func (a *App) rateLimitedList(c *driver.Pan115Client, cid string) (*[]driver.File, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, err
	}
	return c.List(cid)
}

// rateLimitedListPage wraps c.ListPage with rate limiting
func (a *App) rateLimitedListPage(c *driver.Pan115Client, cid string, offset, limit int64) (*[]driver.File, *[]driver.PathInfo, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, nil, err
	}
	return c.ListPage(cid, offset, limit)
}

// rateLimitedQRCodeStart wraps driver.Default().QRCodeStart with rate limiting
func (a *App) rateLimitedQRCodeStart() (*driver.QRCodeSession, error) {
	// ctx := context.Background()
	// if err := a.waitForRateLimit(ctx); err != nil {
	// 	return nil, err
	// }
	c := driver.Default()
	return c.QRCodeStart()
}

// rateLimitedQRCodeStatus wraps driver.Default().QRCodeStatus with rate limiting
func (a *App) rateLimitedQRCodeStatus(session *driver.QRCodeSession) (*driver.QRCodeStatus, error) {
	// ctx := context.Background()
	// if err := a.waitForRateLimit(ctx); err != nil {
	// 	return nil, err
	// }
	c := driver.Default()
	return c.QRCodeStatus(session)
}

// rateLimitedQRCodeLoginWithApp wraps driver.Default().QRCodeLoginWithApp with rate limiting
func (a *App) rateLimitedQRCodeLoginWithApp(session *driver.QRCodeSession, app driver.LoginApp) (*driver.Credential, error) {
	// ctx := context.Background()
	// if err := a.waitForRateLimit(ctx); err != nil {
	// 	return nil, err
	// }
	c := driver.Default()
	return c.QRCodeLoginWithApp(session, app)
}

// rateLimitedDownload2Redirect wraps c.Download with rate limiting
func (a *App) rateLimitedDownload2Redirect(c *driver.Pan115Client, pickCode string, args LinkArgs) (*driver.DownloadInfo, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, err
	}
	userAgent := args.Header.Get("User-Agent")
	return c.DownloadWithUA(pickCode, userAgent)
}

func (a *App) rateLimitedDownload(c *driver.Pan115Client, pickCode string) (*driver.DownloadInfo, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, err
	}
	return c.Download(pickCode)
}

// rateLimitedBehaviorDetailWithApp wraps c.BehaviorDetailWithApp with rate limiting
func (a *App) rateLimitedBehaviorDetailWithApp(c *driver.Pan115Client, app string, payload interface{}) (*driver.BehaviorDetailResp, error) {
	// ctx := context.Background()
	// if err := a.waitForRateLimit(ctx); err != nil {
	// 	return nil, err
	// }
	return c.BehaviorDetailWithApp(app, payload)
}

// rateLimitedStat wraps c.Stat with rate limiting
func (a *App) rateLimitedStat(c *driver.Pan115Client, fileID string) (*driver.FileStatInfo, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, err
	}
	return c.Stat(fileID)
}

// rateLimitedMove wraps c.Move with rate limiting
func (a *App) rateLimitedMove(c *driver.Pan115Client, targetCID string, fileIDs ...string) error {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return err
	}
	return c.Move(targetCID, fileIDs...)
}

// rateLimitedRename wraps c.Rename with rate limiting
func (a *App) rateLimitedRename(c *driver.Pan115Client, fileID, newName string) error {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return err
	}
	return c.Rename(fileID, newName)
}

// rateLimitedMkdir wraps c.Mkdir with rate limiting
func (a *App) rateLimitedMkdir(c *driver.Pan115Client, parentCID, dirName string) (string, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return "", err
	}
	return c.Mkdir(parentCID, dirName)
}

// DirName2CID
func (a *App) rateLimitedDirName2CID(c *driver.Pan115Client, dirName string) (*driver.APIGetDirIDResp, error) {
	ctx := context.Background()
	if err := a.waitForRateLimit(ctx); err != nil {
		return nil, err
	}
	return c.DirName2CID(dirName)
}
