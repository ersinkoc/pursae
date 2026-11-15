package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Two checkouts of one session (a double submit, a retried request) must not both adopt the order
// they read: the session is not locked, so the claim is made in the save's transaction.

// gooSessionCopy is a second request of the same visitor: its own context holding a copy of the
// session as it was loaded (what a concurrent request reads from the database).
func gooSessionCopy(t *testing.T, c *gin.Context, srv *Server) *gin.Context {
	t.Helper()
	b, err := json.Marshal(srv.gooSessionFor(c).Data)
	if err != nil {
		t.Fatal(err)
	}
	sess := &gooSession{Key: srv.gooSessionFor(c).Key}
	if err := json.Unmarshal(b, &sess.Data); err != nil {
		t.Fatal(err)
	}
	c2 := gooTestCtx()
	c2.Set(gooCtxSession, sess)
	return c2
}

func TestGooCheckoutClaimDraftOnce(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	d := &gooOrder{Status: "checkout-draft", CreatedVia: "store-api", Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{}}
	if err := srv.gooSaveOrder(ctx, d, true); err != nil {
		t.Fatal(err)
	}
	claim := func() bool {
		var ok bool
		if err := pgx.BeginFunc(ctx, srv.store.Pool, func(tx pgx.Tx) error {
			var err error
			ok, err = srv.gooClaimDraftTx(ctx, tx, d.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !claim() {
		t.Fatal("first claim of the draft failed")
	}
	if claim() {
		t.Error("the draft was claimed twice")
	}
}

func TestGooCheckoutClaimAwaitingOrderVersion(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Reused", 10, nil)
	o := gooRaceOrder(t, srv, "r@example.test", 0, map[*gooProduct]int{p: 1})
	a, _ := srv.gooGetOrder(ctx, o.ID)
	b, _ := srv.gooGetOrder(ctx, o.ID)
	read := a.DateModified // the version both checkouts read
	save := func(x *gooOrder) error {
		return pgx.BeginFunc(ctx, srv.store.Pool, func(tx pgx.Tx) error {
			ok, err := srv.gooClaimAwaitingOrderTx(ctx, tx, x.ID, read)
			if err != nil {
				return err
			}
			if !ok {
				return errGooOrderInProgress
			}
			return srv.gooSaveOrderTx(ctx, tx, x, true)
		})
	}
	if err := save(a); err != nil {
		t.Fatalf("first reuse: %v", err)
	}
	if err := save(b); !errors.Is(err, errGooOrderInProgress) {
		t.Errorf("second reuse of the same version: %v", err)
	}
}

// Both requests reserve the gift card (its balance covers two orders); only one places the draft
// order, and the other's debit is given back.
func TestGooCheckoutDoubleSubmitDraft(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	lamp := gooMakeSimple(t, srv, "Lamp", 100, func(p *gooProduct) { p.Virtual = true })
	card := &gcCard{Initial: 500, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, lamp.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gcApply(c, card.Code); err != nil {
		t.Fatal(err)
	}
	draft := srv.gooStoreDraftOrder(c)
	if draft == nil {
		t.Fatal("no draft")
	}
	c1, c2 := gooSessionCopy(t, c, srv), gooSessionCopy(t, c, srv)
	var mu sync.Mutex
	var placed []*gooOrder
	var failures []string
	run := func(rc *gin.Context) func() {
		return func() {
			o, errs := gxCheckout(t, srv, rc, "goo_test")
			mu.Lock()
			defer mu.Unlock()
			if len(errs) == 0 && o != nil {
				placed = append(placed, o)
			} else {
				failures = append(failures, strings.Join(errs, "; "))
			}
		}
	}
	gooRace(run(c1), run(c2))
	if len(placed) != 1 {
		t.Fatalf("placed %d orders (failures %v)", len(placed), failures)
	}
	if placed[0].ID != draft.ID {
		t.Errorf("the order is %d, not the draft %d", placed[0].ID, draft.ID)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "already being processed") {
		t.Errorf("the losing checkout: %v", failures)
	}
	g, _ := srv.gcGet(ctx, card.ID)
	gooAlmost(t, "card balance", g.Balance, 400)
	used := 0
	for _, a := range srv.gcActivities(ctx, card.ID) {
		if a.Type == "used" {
			used++
		}
	}
	if used != 1 {
		t.Errorf("used activity rows: %d", used)
	}
}
