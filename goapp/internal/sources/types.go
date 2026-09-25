package sources

// Normalized datasets: what widgets, metrics and rules actually consume,
// after each service's raw API shape is flattened here. Typed structs
// (rather than the Python sources' raw dicts) so the metrics/rules layer
// gets compile-time field checks.

// ── Kimai ──

type KimaiSheet struct {
	ID         int64
	Begin      string
	End        string
	Minutes    int
	Rate       float64
	Billable   bool
	Exported   bool
	ProjectID  int64
	CustomerID int64
	Activity   string
	UserID     int64
}

type KimaiProject struct {
	ID            int64
	Name          string
	CustomerID    int64
	Budget        float64
	TimeBudgetMin int
	BudgetType    string
	End           string
	UsedMoney     float64
	UsedMinutes   int
}

type KimaiCustomer struct {
	ID   int64
	Name string
}

type KimaiAbsence struct {
	Start   string
	End     string
	Type    string
	Status  string
	HalfDay bool
}

type KimaiHoliday struct {
	Date    string
	Name    string
	HalfDay bool
}

type KimaiDataset struct {
	URL           string
	Timesheets    []KimaiSheet
	Active        []KimaiSheet
	Projects      []KimaiProject
	Customers     []KimaiCustomer
	Absences      []KimaiAbsence
	Holidays      []KimaiHoliday
	HolidayBundle bool
}

// ── Invoice Ninja ──

type NinjaInvoice struct {
	ID       int64
	Number   string
	ClientID int64
	Status   string
	Date     string
	DueDate  string
	Amount   float64
	Balance  float64
	Taxes    float64
	Net      float64
}

type NinjaPayment struct {
	ID       int64
	Date     string
	Amount   float64
	ClientID int64
}

type NinjaClient struct {
	ID        int64
	Name      string
	VATNumber string
	CountryID string
}

type NinjaExpense struct {
	ID       int64
	Date     string
	Amount   float64
	Tax      float64
	Notes    string
	VendorID int64
}

type NinjaQuote struct {
	ID       int64
	Number   string
	ClientID int64
	Status   string
	Date     string
	Amount   float64
}

type NinjaRecurring struct {
	ID              int64
	Number          string
	ClientID        int64
	Active          bool
	NextSendDate    string
	RemainingCycles int
	Amount          float64
}

type NinjaDataset struct {
	URL           string
	Currency      string
	Invoices      []NinjaInvoice
	Payments      []NinjaPayment
	Clients       []NinjaClient
	Expenses      []NinjaExpense
	Quotes        []NinjaQuote
	Recurring     []NinjaRecurring
	HomeCountryID string
}

// ── Snipe-IT ──

type SnipeAsset struct {
	ID              int64
	Name            string
	Tag             string
	Model           string
	Category        string
	Status          string
	Deployable      bool
	Assigned        bool
	PurchaseDate    string
	PurchaseCost    float64
	WarrantyExpires string
	EOLDate         string
	NextAudit       string
	LastChange      string
}

type SnipeLicense struct {
	ID      int64
	Name    string
	Expires string
	Seats   int
	Free    int
}

type SnipeConsumable struct {
	ID        int64
	Name      string
	Remaining int
	Min       int
}

type SnipeDataset struct {
	URL          string
	Assets       []SnipeAsset
	Licenses     []SnipeLicense
	Consumables  []SnipeConsumable
	AuditOverdue []int64
}

// ── Dawarich ──

type DawarichArea struct {
	ID     int64
	Name   string
	Lat    float64
	Lon    float64
	Radius float64
}

type DawarichVisit struct {
	ID      int64
	Start   string
	End     string
	Minutes int
	AreaID  int64
	Name    string
	Lat     *float64
	Lon     *float64
}

type DawarichDataset struct {
	URL       string
	Areas     []DawarichArea
	Visits    []DawarichVisit
	Stats     map[string]any
	LastPoint string
}
