package server

// The mailers of WPGO Mail SMTP with WP Mail SMTP's slugs, setting keys and field labels. The
// settings screens, the setup wizard and the WPMS_* environment overrides are generated from it.

type msField struct {
	Key, Label, Type, Desc, Placeholder, Default string // Type: text, password, select, radio, checkbox
	Options                                      [][2]string
}

type msMailerDef struct {
	Slug, Title, Desc, Docs string
	Recommended, OAuth, Pro bool
	Color                   string // tile accent
	Fields                  []msField
}

var msRegions = [][2]string{{"US", "US"}, {"EU", "EU"}}

var msSESRegions = [][2]string{{"us-east-1", "US East (N. Virginia)"}, {"us-east-2", "US East (Ohio)"}, {"us-west-1", "US West (N. California)"},
	{"us-west-2", "US West (Oregon)"}, {"ca-central-1", "Canada (Central)"}, {"eu-west-1", "EU (Ireland)"}, {"eu-west-2", "EU (London)"},
	{"eu-west-3", "EU (Paris)"}, {"eu-central-1", "EU (Frankfurt)"}, {"eu-south-1", "EU (Milan)"}, {"eu-north-1", "EU (Stockholm)"},
	{"ap-south-1", "Asia Pacific (Mumbai)"}, {"ap-northeast-1", "Asia Pacific (Tokyo)"}, {"ap-northeast-2", "Asia Pacific (Seoul)"},
	{"ap-northeast-3", "Asia Pacific (Osaka)"}, {"ap-southeast-1", "Asia Pacific (Singapore)"}, {"ap-southeast-2", "Asia Pacific (Sydney)"},
	{"sa-east-1", "South America (São Paulo)"}, {"me-south-1", "Middle East (Bahrain)"}, {"af-south-1", "Africa (Cape Town)"}}

var msMailers = []msMailerDef{
	{Slug: "mail", Title: "Default (none)", Color: "#646970",
		Desc: "You currently have the Default (none) mailer selected, which won't improve email deliverability. WordPress Go then sends with its built-in transport: the WPGO_SMTP_* environment variables, or .eml files in the mail directory on development sites."},
	{Slug: "sendlayer", Title: "SendLayer", Recommended: true, Color: "#0d6efd", Docs: "https://sendlayer.com",
		Desc:   "SendLayer is our #1 recommended mailer. Its affordable pricing and simple setup make it the perfect choice for WordPress sites.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from SendLayer: Get API Key."}}},
	{Slug: "smtpcom", Title: "SMTP.com", Recommended: true, Color: "#ff6a00", Docs: "https://www.smtp.com",
		Desc: "SMTP.com is a recommended transactional email service. With over 22 years of email delivery expertise, SMTP.com has a reputation for being one of the most reliable senders on the internet.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from SMTP.com: Get API Key."},
			{Key: "channel", Label: "Sender Name", Type: "text", Desc: "Follow this link to get a Sender Name from SMTP.com: Get Sender Name."}}},
	{Slug: "sendinblue", Title: "Brevo", Recommended: true, Color: "#0b996e", Docs: "https://www.brevo.com",
		Desc: "Brevo (formerly Sendinblue) is one of our recommended mailers. It's a transactional email provider with scalable price plans, so it's suitable for any size of business.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key: Get v3 API Key."},
			{Key: "domain", Label: "Sending Domain", Type: "text", Desc: "Please input the sending domain/subdomain you configured in your Brevo dashboard."}}},
	{Slug: "amazonses", Title: "Amazon SES", Pro: true, Color: "#ff9900", Docs: "https://aws.amazon.com/ses/",
		Desc: "Amazon Simple Email Service (SES) is a cost-effective email service. Create an IAM user with the AmazonSESFullAccess permission and use its access keys; verify the From address or domain in the region below.",
		Fields: []msField{{Key: "client_id", Label: "Access Key ID", Type: "text"}, {Key: "client_secret", Label: "Secret Access Key", Type: "password"},
			{Key: "region", Label: "Region", Type: "select", Options: msSESRegions, Default: "us-east-1", Desc: "Please select the Amazon SES API region which is the closest to where your website is hosted."}}},
	{Slug: "elasticemail", Title: "Elastic Email", Color: "#2560a4", Docs: "https://elasticemail.com",
		Desc:   "Elastic Email is a cloud-based email platform for transactional and marketing e-mail.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from Elastic Email: Get API Key."}}},
	{Slug: "gmail", Title: "Google / Gmail", OAuth: true, Color: "#ea4335", Docs: "https://console.cloud.google.com",
		Desc:   "Our Gmail mailer works with any Gmail or Google Workspace account via the Google API. Create an OAuth client (Web application) in the Google Cloud Console, enable the Gmail API, and add the redirect URI below as an authorized redirect URI.",
		Fields: []msField{{Key: "client_id", Label: "Client ID", Type: "text"}, {Key: "client_secret", Label: "Client Secret", Type: "password"}}},
	{Slug: "mailgun", Title: "Mailgun", Color: "#c02126", Docs: "https://www.mailgun.com",
		Desc: "Mailgun is a transactional email provider.",
		Fields: []msField{{Key: "api_key", Label: "Mailgun API Key", Type: "password", Desc: "Follow this link to get a Mailgun API Key. Generate a key in the \"Mailgun API Keys\" section."},
			{Key: "domain", Label: "Domain Name", Type: "text", Desc: "Follow this link to get a Domain Name from Mailgun: Get a Domain Name."},
			{Key: "region", Label: "Region", Type: "radio", Options: msRegions, Default: "US", Desc: "Define which endpoint you want to use for sending messages. If you are operating under EU laws, you may be required to use EU region."}}},
	{Slug: "mailjet", Title: "Mailjet", Color: "#a0e6c8", Docs: "https://www.mailjet.com",
		Desc: "Mailjet is a cloud-based email service platform that enables businesses to send marketing and transactional emails.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password"}, {Key: "secret_key", Label: "Secret Key", Type: "password",
			Desc: "Follow this link to get the API key and Secret key from Mailjet: API Key Management."}}},
	{Slug: "mailersend", Title: "MailerSend", Color: "#4f46e5", Docs: "https://www.mailersend.com",
		Desc: "MailerSend is a transactional email service with a generous free plan.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from MailerSend: Get API Key."},
			{Key: "has_pro_plan", Label: "Professional Plan", Type: "checkbox", Desc: "Activate if you have a Professional or higher plan with MailerSend (allows more than 500 recipients and custom headers)."}}},
	{Slug: "mandrill", Title: "Mandrill", Color: "#c2185b", Docs: "https://mandrillapp.com",
		Desc:   "Mandrill is Mailchimp's transactional email service.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from Mandrill: Get API Key."}}},
	{Slug: "outlook", Title: "365 / Outlook", OAuth: true, Pro: true, Color: "#0078d4", Docs: "https://portal.azure.com",
		Desc:   "Outlook and Microsoft 365 mailer: register an application in Microsoft Entra ID (Azure), add the Mail.Send and User.Read delegated permissions and a client secret, and add the redirect URI below (Web platform).",
		Fields: []msField{{Key: "client_id", Label: "Application ID", Type: "text"}, {Key: "client_secret", Label: "Application Password", Type: "password"}}},
	{Slug: "postmark", Title: "Postmark", Color: "#ffde00", Docs: "https://postmarkapp.com",
		Desc: "Postmark is a transactional email provider that offers great deliverability and accessible pricing for any business.",
		Fields: []msField{{Key: "server_api_token", Label: "Server API Token", Type: "password", Desc: "Create a server API token in Postmark: Servers › your server › API Tokens."},
			{Key: "message_stream", Label: "Message Stream ID", Type: "text", Placeholder: "outbound", Desc: "Optional. Leave empty to use the default transactional stream (outbound)."}}},
	{Slug: "resend", Title: "Resend", Color: "#000000", Docs: "https://resend.com",
		Desc:   "Resend is an email API for developers.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from Resend: Get API Key."}}},
	{Slug: "sendgrid", Title: "SendGrid", Color: "#1a82e2", Docs: "https://sendgrid.com",
		Desc: "SendGrid is a popular transactional email provider.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from SendGrid: Create API Key. Give it \"Mail Send\" permission."},
			{Key: "domain", Label: "Sending Domain", Type: "text", Desc: "Please input the sending domain/subdomain you configured in your SendGrid dashboard."}}},
	{Slug: "smtp2go", Title: "SMTP2GO", Color: "#2a74b9", Docs: "https://www.smtp2go.com",
		Desc:   "SMTP2GO provides scalable and reliable email delivery services.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Generate an API key under Sending › API Keys in SMTP2GO."}}},
	{Slug: "sparkpost", Title: "SparkPost", Color: "#fa6423", Docs: "https://www.sparkpost.com",
		Desc: "SparkPost is a transactional email provider.",
		Fields: []msField{{Key: "api_key", Label: "API Key", Type: "password", Desc: "Follow this link to get an API Key from SparkPost (Transmissions: Read/Write)."},
			{Key: "region", Label: "Region", Type: "radio", Options: msRegions, Default: "US", Desc: "Select your SparkPost account region."}}},
	{Slug: "zoho", Title: "Zoho Mail", OAuth: true, Pro: true, Color: "#e42527", Docs: "https://api-console.zoho.com",
		Desc: "Zoho Mail: create a \"Server-based Application\" in the Zoho API Console with the redirect URI below.",
		Fields: []msField{{Key: "domain", Label: "Region", Type: "select", Default: "com", Options: [][2]string{{"com", "United States (US)"}, {"eu", "Europe (EU)"},
			{"in", "India (IN)"}, {"com.cn", "China (CN)"}, {"com.au", "Australia (AU)"}, {"jp", "Japan (JP)"}, {"ca", "Canada (CA)"}, {"sa", "Saudi Arabia (SA)"}}},
			{Key: "client_id", Label: "Client ID", Type: "text"}, {Key: "client_secret", Label: "Client Secret", Type: "password"}}},
	{Slug: "smtp", Title: "Other SMTP", Color: "#3c434a",
		Desc: "Use the SMTP details provided by your hosting provider or email service. To see recommended settings for the popular services as well as troubleshooting tips, check out the WP Mail SMTP documentation.",
		Fields: []msField{{Key: "host", Label: "SMTP Host", Type: "text"},
			{Key: "encryption", Label: "Encryption", Type: "radio", Default: "none", Options: [][2]string{{"none", "None"}, {"ssl", "SSL"}, {"tls", "TLS"}},
				Desc: "For most servers TLS is the recommended option. If your SMTP provider offers both SSL and TLS options, we recommend using TLS."},
			{Key: "port", Label: "SMTP Port", Type: "number", Default: "587"},
			{Key: "autotls", Label: "Auto TLS", Type: "checkbox", Default: "1", Desc: "By default, TLS encryption is automatically used if the server supports it (recommended). In some cases, due to server misconfigurations, this can cause issues and may need to be disabled."},
			{Key: "auth", Label: "Authentication", Type: "checkbox", Default: "1"},
			{Key: "user", Label: "SMTP Username", Type: "text"},
			{Key: "pass", Label: "SMTP Password", Type: "password", Desc: "The password is encrypted in the database with the server secret. You can also set it with the WPMS_SMTP_PASS environment variable."}}},
}

func msMailerDefBySlug(slug string) *msMailerDef {
	for i := range msMailers {
		if msMailers[i].Slug == slug {
			return &msMailers[i]
		}
	}
	return nil
}

func msMailerTitle(slug string) string {
	if d := msMailerDefBySlug(slug); d != nil {
		return d.Title
	}
	if slug == "" {
		return "Default (none)"
	}
	return slug
}
