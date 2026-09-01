package aimlapi

// UI strings shared by the first-run and provider-wizard onboarding surfaces.

const (
	// Path A — existing API key. Pick-path prompt + option labels.
	MsgAPIKeyInputPrompt = "Enter your aimlapi.com key."
	MsgAPIKeyInvalid     = "API key is invalid. Please make sure you enter a valid aimlapi.com key."
	MsgPickPathPrompt    = "Do you have aimlapi.com key?"
	MsgPickPathHaveKey   = "I already have aimlapi.com key"
	MsgPickPathHaveHint  = "Proceed to paste the key"
	MsgPickPathNewUser   = "I am a new user"
	MsgPickPathNewHint   = "One click set up"
	// Path B — email.
	MsgEnterEmail           = "Enter your email.\nTo access aimlapi.com dashboard."
	MsgEmailInvalid         = "Email format is incorrect."
	MsgAccountActionInvalid = "aimlapi.com returned an unsupported account action. Please try again."
	// %s = email
	MsgCodeSent      = "We sent a 6-digit code to %s.\nEnter it below to continue."
	MsgCodeIncorrect = "Code you've entered is incorrect."

	// Balance check.
	MsgLowBalance     = "Your aimlapi.com balance is running low.\nIt is recommended to top up your balance."
	MsgEverythingRuns = "Everything is ready."
	// Low-balance choices (spec wording).
	MsgLowBalanceTopUp = "Sure, let's do that"
	MsgLowBalanceSkip  = "I'll skip topping up the balance for now"

	// Top-up workflow.
	MsgTopUpPrompt    = "Add credits.\nEnter an amount (min $20)."
	MsgAmountRequired = "Please enter a top-up amount."
	// %s = checkout URL
	MsgTopUpBrowserFallback = "If the browser did not open automatically please use this link to top up your account: %s"
	MsgTopUpFailed          = "Top up failed. Please try again."
	MsgTopUpSuccess         = "Top-up successful."

	// Success — key delivered.
	// %s = amount in dollars (e.g. "25")
	MsgTopUpAddedFmt = "$%s has been added to your balance."
	// %s = email
	MsgSuccessMagicLink = "We've emailed you a magic link to %s. Use it to access your aimlapi.com account and review your usage and balance."
)
