// GoToSocial
// Copyright (C) GoToSocial Authors admin@gotosocial.org
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	apiutil "github.com/superseriousbusiness/gotosocial/internal/api/util"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/oauth"
)

func (m *Module) threadGETHandler(c *gin.Context) {
	ctx := c.Request.Context()

	// Don't require auth for web endpoints,
	// but do take it if it was provided.
	authed, err := oauth.Authed(c, false, false, false, false)
	if err != nil {
		apiutil.WebErrorHandler(c, gtserror.NewErrorUnauthorized(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	// requestingAccount may be nil, depending
	// on how they authed (if at all).
	requestingAccount := authed.Account

	// We'll need the instance later, and we can also use it
	// before then to make it easier to return a web error.
	instance, err := m.processor.InstanceGetV1(ctx)
	if err != nil {
		apiutil.WebErrorHandler(c, gtserror.NewErrorInternalError(err), m.processor.InstanceGetV1)
		return
	}

	// Return instance we already got from the db,
	// don't try to fetch it again when erroring.
	instanceGet := func(ctx context.Context) (*apimodel.InstanceV1, gtserror.WithCode) {
		return instance, nil
	}

	// Parse account targetUsername and status ID from the URL.
	targetUsername, errWithCode := apiutil.ParseWebUsername(c.Param(apiutil.WebUsernameKey))
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, instanceGet)
		return
	}

	targetStatusID, errWithCode := apiutil.ParseWebStatusID(c.Param(apiutil.WebStatusIDKey))
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, instanceGet)
		return
	}

	// Normalize requested username + status ID:
	//
	//   - Usernames on our instance are (currently) always lowercase.
	//   - StatusIDs on our instance are (currently) always ULIDs.
	//
	// todo: Update this logic when different username patterns
	// are allowed, and/or when status slugs are introduced.
	targetUsername = strings.ToLower(targetUsername)
	targetStatusID = strings.ToUpper(targetStatusID)

	// Ensure status is actually from a local account; don't
	// render threads from statuses that don't belong to us.
	_, errWithCode = m.processor.Account().GetLocalByUsername(ctx, requestingAccount, targetUsername)
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, instanceGet)
		return
	}

	// Get the status itself from the processor using provided ID.
	status, errWithCode := m.processor.Status().Get(ctx, requestingAccount, targetStatusID)
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, instanceGet)
		return
	}

	if status.Account.Username != targetUsername {
		err := errors.New("path username not equal to status author username")
		apiutil.WebErrorHandler(c, gtserror.NewErrorNotFound(err), instanceGet)
		return
	}

	formats := []string{
		string(apiutil.TextHTML),
		string(apiutil.AppActivityJSON),
		string(apiutil.AppActivityLDJSON),
	}

	// If we're getting an AP request on this endpoint we
	// should render the status's AP representation instead.
	accept := apiutil.NegotiateFormat(c, formats...)
	if accept == string(apiutil.AppActivityJSON) || accept == string(apiutil.AppActivityLDJSON) {
		m.returnAPStatus(c, targetUsername, targetStatusID, accept)
		return
	}

	context, errWithCode := m.processor.Status().ContextGet(ctx, requestingAccount, targetStatusID)
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, instanceGet)
		return
	}

	stylesheets := []string{
		assetsPathPrefix + "/Fork-Awesome/css/fork-awesome.min.css",
		distPathPrefix + "/status.css",
	}
	if config.GetAccountsAllowCustomCSS() {
		stylesheets = append(stylesheets, "/@"+targetUsername+"/custom.css")
	}

	c.HTML(http.StatusOK, "thread.tmpl", gin.H{
		"instance":    instance,
		"status":      status,
		"context":     context,
		"ogMeta":      ogBase(instance).withStatus(status),
		"stylesheets": stylesheets,
		"javascript":  []string{distPathPrefix + "/frontend.js"},
	})
}

func (m *Module) returnAPStatus(
	c *gin.Context,
	targetUsername string,
	targetStatusID string,
	accept string,
) {
	status, errWithCode := m.processor.Fedi().StatusGet(c.Request.Context(), targetUsername, targetStatusID)
	if errWithCode != nil {
		apiutil.WebErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	b, mErr := json.Marshal(status)
	if mErr != nil {
		err := fmt.Errorf("could not marshal json: %s", mErr)
		apiutil.WebErrorHandler(c, gtserror.NewErrorInternalError(err), m.processor.InstanceGetV1)
		return
	}

	c.Data(http.StatusOK, accept, b)
}
