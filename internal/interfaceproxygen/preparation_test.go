package interfaceproxygen_test

const generatedPreparationTests = `
func TestGeneratedPreparationSharesOneBudget(t *testing.T) {
	for _, mode := range []string{"success", "preparation-timeout", "target-timeout", "earlier-caller", "no-timeout", "invalid", "panic"} {
		t.Run(mode, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			timeout, delay := 2*time.Second, time.Second
			if mode == "preparation-timeout" { delay = 3*time.Second }
			if mode == "no-timeout" { timeout, delay = 0, 31*time.Second }
			proxy.TestRequestBarrier = func() { time.Sleep(delay); if mode == "panic" { panic("private preparation panic") } }
			defer func() { proxy.TestRequestBarrier = func() {} }()
			entered := false
			token := capability.MustParseContract[contract.Request, contract.Response]("order.create/v1")
			endpoint, err := invocation.NewEndpoint(token, func(ctx context.Context, request contract.Request) (contract.Response, error) {
				entered = true
				deadline, bounded := ctx.Deadline()
				if timeout == 0 {
					if bounded { t.Error("absence added a hidden deadline") }
					time.Sleep(31*time.Second)
				} else if !bounded || time.Until(deadline) != time.Second { t.Errorf("preparation did not consume the one budget: %v", time.Until(deadline)) }
				if mode == "target-timeout" { <-ctx.Done(); return contract.Response{}, ctx.Err() }
				return contract.Response{Value: "response:" + request.Value}, nil
			})
			if err != nil { t.Fatal(err) }
			build, err := invocation.NewModuleBuild("example.com/proxyfixture", "v1.0.0", "")
			if err != nil { t.Fatal(err) }
			binding, err := invocation.NewBinding(invocation.BindingOptions{
				Policy: invocation.Policy{SchemaVersion: 1, CompilerVersion: 1, DefaultsVersion: 1, Timeout: timeout, ConcurrencyLimit: 1, Retry: invocation.RetryPolicy{MaxAttempts: 1}},
				Kind: invocation.BindingKindImplementation, Constructor: "example.com/proxyfixture/implementation.New",
				ModuleBuild: build, SelectionReason: invocation.SelectionReasonUniqueCompatible,
				ContractDigest: sha256.Sum256([]byte("order.create/v1")),
			}, endpoint)
			if err != nil { t.Fatal(err) }
			catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
			if err != nil { t.Fatal(err) }
			dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: 1})
			if err != nil { t.Fatal(err) }
			if err := dispatcher.Publish(catalog); err != nil { t.Fatal(err) }
			if err := dispatcher.OpenAdmission(); err != nil { t.Fatal(err) }
			handle, err := invocation.NewHandle(dispatcher, token, true)
			if err != nil { t.Fatal(err) }
			ctx := context.Background()
			if mode == "earlier-caller" { var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, time.Second/2); defer cancel() }
			request := contract.Request{Value: "request"}
			if mode == "invalid" { request.Value = "short" }
			start := time.Now()
			response, err := proxy.New(handle).Create(ctx, request)
			bounded, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if drainErr := dispatcher.Drain(bounded); drainErr != nil { t.Fatal(drainErr) }
			if mode == "success" || mode == "no-timeout" {
				if err != nil || !entered || response.Value != "response:request" { t.Fatalf("successful preparation: %#v, %v", response, err) }
				return
			}
			var boundary *invocation.Error
			if !errors.As(err, &boundary) || response != (contract.Response{}) { t.Fatalf("unsafe preparation outcome: %#v, %v", response, err) }
			var validation *proxy.ValueError
			switch mode {
			case "invalid":
				if !errors.As(err, &validation) || validation.Side() != "request" || validation.Unwrap() != proxy.TestReturnedBoundary.Load() || boundary.Attempts() != 0 { t.Fatalf("request details/evidence not restored: %v", err) }
			case "panic":
				if boundary.Code() != invocation.ErrorInternal || errors.As(err, &validation) { t.Fatalf("panic outcome: %v", err) }
			default:
				if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &validation) { t.Fatalf("deadline outcome: %v", err) }
			}
			if mode == "target-timeout" {
				if !entered || time.Since(start) != 2*time.Second || boundary.Attempts() != 1 { t.Fatalf("target received a fresh budget: %v, %v", time.Since(start), err) }
			} else if entered || boundary.Completion() != invocation.CompletionNotStarted || boundary.Attempts() != 0 { t.Fatalf("preparation failure entered target: %v", err) }
		}) })
	}
}
`
