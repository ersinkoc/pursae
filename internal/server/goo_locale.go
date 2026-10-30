package server

import (
	"sort"
	"strings"
)

// Goocommerce's locale data: countries (ISO 3166-1 alpha-2), the states WooCommerce knows for the
// countries that need one, continents for shipping zones and the currencies with their symbols.

const gooCountryList = "AD:Andorra|AE:United Arab Emirates|AF:Afghanistan|AG:Antigua and Barbuda|AI:Anguilla|AL:Albania|AM:Armenia|AO:Angola|AQ:Antarctica|AR:Argentina|AS:American Samoa|AT:Austria|AU:Australia|AW:Aruba|AX:Åland Islands|AZ:Azerbaijan|BA:Bosnia and Herzegovina|BB:Barbados|BD:Bangladesh|BE:Belgium|BF:Burkina Faso|BG:Bulgaria|BH:Bahrain|BI:Burundi|BJ:Benin|BL:St Barthelemy|BM:Bermuda|BN:Brunei|BO:Bolivia|BQ:Caribbean NL|BR:Brazil|BS:Bahamas|BT:Bhutan|BV:Bouvet Island|BW:Botswana|BY:Belarus|BZ:Belize|CA:Canada|CC:Cocos (Keeling) Islands|CD:Congo (Kinshasa)|CF:Central African Rep.|CG:Congo (Brazzaville)|CH:Switzerland|CI:Côte d’Ivoire|CK:Cook Islands|CL:Chile|CM:Cameroon|CN:China|CO:Colombia|CR:Costa Rica|CU:Cuba|CV:Cape Verde|CW:Curaçao|CX:Christmas Island|CY:Cyprus|CZ:Czech Republic|DE:Germany|DJ:Djibouti|DK:Denmark|DM:Dominica|DO:Dominican Republic|DZ:Algeria|EC:Ecuador|EE:Estonia|EG:Egypt|EH:Western Sahara|ER:Eritrea|ES:Spain|ET:Ethiopia|FI:Finland|FJ:Fiji|FK:Falkland Islands|FM:Micronesia|FO:Faroe Islands|FR:France|GA:Gabon|GB:United Kingdom (UK)|GD:Grenada|GE:Georgia|GF:French Guiana|GG:Guernsey|GH:Ghana|GI:Gibraltar|GL:Greenland|GM:Gambia|GN:Guinea|GP:Guadeloupe|GQ:Equatorial Guinea|GR:Greece|GS:South Georgia/Sandwich Islands|GT:Guatemala|GU:Guam|GW:Guinea-Bissau|GY:Guyana|HK:Hong Kong|HM:Heard Island and McDonald Islands|HN:Honduras|HR:Croatia|HT:Haiti|HU:Hungary|ID:Indonesia|IE:Ireland|IL:Israel|IM:Isle of Man|IN:India|IO:British Indian Ocean Territory|IQ:Iraq|IR:Iran|IS:Iceland|IT:Italy|JE:Jersey|JM:Jamaica|JO:Jordan|JP:Japan|KE:Kenya|KG:Kyrgyzstan|KH:Cambodia|KI:Kiribati|KM:Comoros|KN:Saint Kitts and Nevis|KP:North Korea|KR:South Korea|KW:Kuwait|KY:Cayman Islands|KZ:Kazakhstan|LA:Laos|LB:Lebanon|LC:St Lucia|LI:Liechtenstein|LK:Sri Lanka|LR:Liberia|LS:Lesotho|LT:Lithuania|LU:Luxembourg|LV:Latvia|LY:Libya|MA:Morocco|MC:Monaco|MD:Moldova|ME:Montenegro|MF:Saint Martin (French part)|MG:Madagascar|MH:Marshall Islands|MK:North Macedonia|ML:Mali|MM:Myanmar|MN:Mongolia|MO:Macau|MP:Northern Mariana Islands|MQ:Martinique|MR:Mauritania|MS:Montserrat|MT:Malta|MU:Mauritius|MV:Maldives|MW:Malawi|MX:Mexico|MY:Malaysia|MZ:Mozambique|NA:Namibia|NC:New Caledonia|NE:Niger|NF:Norfolk Island|NG:Nigeria|NI:Nicaragua|NL:Netherlands|NO:Norway|NP:Nepal|NR:Nauru|NU:Niue|NZ:New Zealand|OM:Oman|PA:Panama|PE:Peru|PF:French Polynesia|PG:Papua New Guinea|PH:Philippines|PK:Pakistan|PL:Poland|PM:Saint Pierre and Miquelon|PN:Pitcairn|PR:Puerto Rico|PS:Palestine|PT:Portugal|PW:Palau|PY:Paraguay|QA:Qatar|RE:Réunion|RO:Romania|RS:Serbia|RU:Russia|RW:Rwanda|SA:Saudi Arabia|SB:Solomon Islands|SC:Seychelles|SD:Sudan|SE:Sweden|SG:Singapore|SH:St Helena|SI:Slovenia|SJ:Svalbard and Jan Mayen|SK:Slovakia|SL:Sierra Leone|SM:San Marino|SN:Senegal|SO:Somalia|SR:Suriname|SS:South Sudan|ST:São Tomé and Príncipe|SV:El Salvador|SX:Sint Maarten (Dutch part)|SY:Syria|SZ:Eswatini|TC:Turks and Caicos Islands|TD:Chad|TF:French S. Terr.|TG:Togo|TH:Thailand|TJ:Tajikistan|TK:Tokelau|TL:East Timor|TM:Turkmenistan|TN:Tunisia|TO:Tonga|TR:Türkiye|TT:Trinidad and Tobago|TV:Tuvalu|TW:Taiwan|TZ:Tanzania|UA:Ukraine|UG:Uganda|UM:US minor outlying islands|US:United States (US)|UY:Uruguay|UZ:Uzbekistan|VA:Vatican City|VC:St Vincent|VE:Venezuela|VG:Virgin Islands (British)|VI:Virgin Islands (US)|VN:Vietnam|VU:Vanuatu|WF:Wallis and Futuna|WS:Samoa|YE:Yemen|YT:Mayotte|ZA:South Africa|ZM:Zambia|ZW:Zimbabwe"

const gooStateList = "US:AL=Alabama;AK=Alaska;AZ=Arizona;AR=Arkansas;CA=California;CO=Colorado;CT=Connecticut;DE=Delaware;DC=District Of Columbia;FL=Florida;GA=Georgia;HI=Hawaii;ID=Idaho;IL=Illinois;IN=Indiana;IA=Iowa;KS=Kansas;KY=Kentucky;LA=Louisiana;ME=Maine;MD=Maryland;MA=Massachusetts;MI=Michigan;MN=Minnesota;MS=Mississippi;MO=Missouri;MT=Montana;NE=Nebraska;NV=Nevada;NH=New Hampshire;NJ=New Jersey;NM=New Mexico;NY=New York;NC=North Carolina;ND=North Dakota;OH=Ohio;OK=Oklahoma;OR=Oregon;PA=Pennsylvania;RI=Rhode Island;SC=South Carolina;SD=South Dakota;TN=Tennessee;TX=Texas;UT=Utah;VT=Vermont;VA=Virginia;WA=Washington;WV=West Virginia;WI=Wisconsin;WY=Wyoming;AA=Armed Forces (AA);AE=Armed Forces (AE);AP=Armed Forces (AP)" +
	"|CA:AB=Alberta;BC=British Columbia;MB=Manitoba;NB=New Brunswick;NL=Newfoundland and Labrador;NT=Northwest Territories;NS=Nova Scotia;NU=Nunavut;ON=Ontario;PE=Prince Edward Island;QC=Quebec;SK=Saskatchewan;YT=Yukon Territory" +
	"|AU:ACT=Australian Capital Territory;NSW=New South Wales;NT=Northern Territory;QLD=Queensland;SA=South Australia;TAS=Tasmania;VIC=Victoria;WA=Western Australia" +
	"|TR:TR01=Adana;TR02=Adıyaman;TR03=Afyon;TR04=Ağrı;TR05=Amasya;TR06=Ankara;TR07=Antalya;TR08=Artvin;TR09=Aydın;TR10=Balıkesir;TR11=Bilecik;TR12=Bingöl;TR13=Bitlis;TR14=Bolu;TR15=Burdur;TR16=Bursa;TR17=Çanakkale;TR18=Çankırı;TR19=Çorum;TR20=Denizli;TR21=Diyarbakır;TR22=Edirne;TR23=Elazığ;TR24=Erzincan;TR25=Erzurum;TR26=Eskişehir;TR27=Gaziantep;TR28=Giresun;TR29=Gümüşhane;TR30=Hakkari;TR31=Hatay;TR32=Isparta;TR33=İçel;TR34=İstanbul;TR35=İzmir;TR36=Kars;TR37=Kastamonu;TR38=Kayseri;TR39=Kırklareli;TR40=Kırşehir;TR41=Kocaeli;TR42=Konya;TR43=Kütahya;TR44=Malatya;TR45=Manisa;TR46=Kahramanmaraş;TR47=Mardin;TR48=Muğla;TR49=Muş;TR50=Nevşehir;TR51=Niğde;TR52=Ordu;TR53=Rize;TR54=Sakarya;TR55=Samsun;TR56=Siirt;TR57=Sinop;TR58=Sivas;TR59=Tekirdağ;TR60=Tokat;TR61=Trabzon;TR62=Tunceli;TR63=Şanlıurfa;TR64=Uşak;TR65=Van;TR66=Yozgat;TR67=Zonguldak;TR68=Aksaray;TR69=Bayburt;TR70=Karaman;TR71=Kırıkkale;TR72=Batman;TR73=Şırnak;TR74=Bartın;TR75=Ardahan;TR76=Iğdır;TR77=Yalova;TR78=Karabük;TR79=Kilis;TR80=Osmaniye;TR81=Düzce" +
	"|IN:AP=Andhra Pradesh;AR=Arunachal Pradesh;AS=Assam;BR=Bihar;CT=Chhattisgarh;GA=Goa;GJ=Gujarat;HR=Haryana;HP=Himachal Pradesh;JK=Jammu and Kashmir;JH=Jharkhand;KA=Karnataka;KL=Kerala;LA=Ladakh;MP=Madhya Pradesh;MH=Maharashtra;MN=Manipur;ML=Meghalaya;MZ=Mizoram;NL=Nagaland;OR=Odisha;PB=Punjab;RJ=Rajasthan;SK=Sikkim;TN=Tamil Nadu;TS=Telangana;TR=Tripura;UK=Uttarakhand;UP=Uttar Pradesh;WB=West Bengal;AN=Andaman and Nicobar Islands;CH=Chandigarh;DH=Dadra and Nagar Haveli and Daman and Diu;DL=Delhi;LD=Lakshadeep;PY=Pondicherry (Puducherry)" +
	"|BR:AC=Acre;AL=Alagoas;AP=Amapá;AM=Amazonas;BA=Bahia;CE=Ceará;DF=Distrito Federal;ES=Espírito Santo;GO=Goiás;MA=Maranhão;MT=Mato Grosso;MS=Mato Grosso do Sul;MG=Minas Gerais;PA=Pará;PB=Paraíba;PR=Paraná;PE=Pernambuco;PI=Piauí;RJ=Rio de Janeiro;RN=Rio Grande do Norte;RS=Rio Grande do Sul;RO=Rondônia;RR=Roraima;SC=Santa Catarina;SP=São Paulo;SE=Sergipe;TO=Tocantins" +
	"|DE:DE-BW=Baden-Württemberg;DE-BY=Bavaria;DE-BE=Berlin;DE-BB=Brandenburg;DE-HB=Bremen;DE-HH=Hamburg;DE-HE=Hesse;DE-MV=Mecklenburg-Vorpommern;DE-NI=Lower Saxony;DE-NW=North Rhine-Westphalia;DE-RP=Rhineland-Palatinate;DE-SL=Saarland;DE-SN=Saxony;DE-ST=Saxony-Anhalt;DE-SH=Schleswig-Holstein;DE-TH=Thuringia" +
	"|ES:C=A Coruña;VI=Araba/Álava;AB=Albacete;A=Alicante;AL=Almería;O=Asturias;AV=Ávila;BA=Badajoz;PM=Baleares;B=Barcelona;BU=Burgos;CC=Cáceres;CA=Cádiz;S=Cantabria;CS=Castellón;CE=Ceuta;CR=Ciudad Real;CO=Córdoba;CU=Cuenca;GI=Girona;GR=Granada;GU=Guadalajara;SS=Gipuzkoa;H=Huelva;HU=Huesca;J=Jaén;LO=La Rioja;GC=Las Palmas;LE=León;L=Lleida;LU=Lugo;M=Madrid;MA=Málaga;ML=Melilla;MU=Murcia;NA=Navarra;OR=Ourense;P=Palencia;PO=Pontevedra;SA=Salamanca;TF=Santa Cruz de Tenerife;SG=Segovia;SE=Sevilla;SO=Soria;T=Tarragona;TE=Teruel;TO=Toledo;V=Valencia;VA=Valladolid;BI=Biscay;ZA=Zamora;Z=Zaragoza" +
	"|MX:DF=Ciudad de México;JA=Jalisco;NL=Nuevo León;AG=Aguascalientes;BC=Baja California;BS=Baja California Sur;CM=Campeche;CS=Chiapas;CH=Chihuahua;CO=Coahuila;CL=Colima;DG=Durango;GT=Guanajuato;GR=Guerrero;HG=Hidalgo;MX=Estado de México;MI=Michoacán;MO=Morelos;NA=Nayarit;OA=Oaxaca;PU=Puebla;QT=Querétaro;QR=Quintana Roo;SL=San Luis Potosí;SI=Sinaloa;SO=Sonora;TB=Tabasco;TM=Tamaulipas;TL=Tlaxcala;VE=Veracruz;YU=Yucatán;ZA=Zacatecas"

// gooContinentList is WooCommerce's continents (shipping zone regions).
const gooContinentList = "AF:Africa:AO,BF,BI,BJ,BW,CD,CF,CG,CI,CM,CV,DJ,DZ,EG,EH,ER,ET,GA,GH,GM,GN,GQ,GW,KE,KM,LR,LS,LY,MA,MG,ML,MR,MU,MW,MZ,NA,NE,NG,RE,RW,SC,SD,SH,SL,SN,SO,SS,ST,SZ,TD,TG,TN,TZ,UG,YT,ZA,ZM,ZW" +
	"|AN:Antarctica:AQ,BV,GS,HM,TF" +
	"|AS:Asia:AE,AF,AM,AZ,BD,BH,BN,BT,CC,CN,CX,CY,GE,HK,ID,IL,IN,IO,IQ,IR,JO,JP,KG,KH,KP,KR,KW,KZ,LA,LB,LK,MM,MN,MO,MV,MY,NP,OM,PH,PK,PS,QA,SA,SG,SY,TH,TJ,TL,TM,TR,TW,UZ,VN,YE" +
	"|EU:Europe:AD,AL,AT,AX,BA,BE,BG,BY,CH,CZ,DE,DK,EE,ES,FI,FO,FR,GB,GG,GI,GR,HR,HU,IE,IM,IS,IT,JE,LI,LT,LU,LV,MC,MD,ME,MK,MT,NL,NO,PL,PT,RO,RS,RU,SE,SI,SJ,SK,SM,UA,VA,XK" +
	"|NA:North America:AG,AI,AW,BB,BL,BM,BQ,BS,BZ,CA,CR,CU,CW,DM,DO,GD,GL,GP,GT,HN,HT,JM,KN,KY,LC,MF,MQ,MS,MX,NI,PA,PM,PR,SV,SX,TC,TT,US,VC,VG,VI" +
	"|OC:Oceania:AS,AU,CK,FJ,FM,GU,KI,MH,MP,NC,NF,NR,NU,NZ,PF,PG,PN,PW,SB,TK,TO,TV,UM,VU,WF,WS" +
	"|SA:South America:AR,BO,BR,CL,CO,EC,FK,GF,GY,PE,PY,SR,UY,VE"

// gooCurrencyList is code:name:symbol for every currency the store can sell in.
const gooCurrencyList = "AED:United Arab Emirates dirham:د.إ|AFN:Afghan afghani:؋|ALL:Albanian lek:L|AMD:Armenian dram:AMD|ARS:Argentine peso:$|AUD:Australian dollar:$|AZN:Azerbaijani manat:₼|BAM:Bosnia and Herzegovina convertible mark:KM|BDT:Bangladeshi taka:৳&nbsp;|BGN:Bulgarian lev:лв.|BHD:Bahraini dinar:.د.ب|BRL:Brazilian real:R$|CAD:Canadian dollar:$|CHF:Swiss franc:CHF|CLP:Chilean peso:$|CNY:Chinese yuan:¥|COP:Colombian peso:$|CZK:Czech koruna:Kč|DKK:Danish krone:kr.|DZD:Algerian dinar:د.ج|EGP:Egyptian pound:EGP|EUR:Euro:€|GBP:Pound sterling:£|GEL:Georgian lari:₾|HKD:Hong Kong dollar:$|HUF:Hungarian forint:Ft|IDR:Indonesian rupiah:Rp|ILS:Israeli new shekel:₪|INR:Indian rupee:₹|IQD:Iraqi dinar:د.ع|IRR:Iranian rial:﷼|ISK:Icelandic króna:kr.|JOD:Jordanian dinar:د.ا|JPY:Japanese yen:¥|KES:Kenyan shilling:KSh|KRW:South Korean won:₩|KWD:Kuwaiti dinar:د.ك|KZT:Kazakhstani tenge:₸|LBP:Lebanese pound:ل.ل|LKR:Sri Lankan rupee:රු|MAD:Moroccan dirham:د.م.|MDL:Moldovan leu:MDL|MXN:Mexican peso:$|MYR:Malaysian ringgit:RM|NGN:Nigerian naira:₦|NOK:Norwegian krone:kr|NPR:Nepalese rupee:₨|NZD:New Zealand dollar:$|OMR:Omani rial:ر.ع.|PEN:Sol:S/|PHP:Philippine peso:₱|PKR:Pakistani rupee:₨|PLN:Polish złoty:zł|QAR:Qatari riyal:ر.ق|RON:Romanian leu:lei|RSD:Serbian dinar:рсд|RUB:Russian ruble:₽|SAR:Saudi riyal:ر.س|SEK:Swedish krona:kr|SGD:Singapore dollar:$|THB:Thai baht:฿|TND:Tunisian dinar:د.ت|TRY:Turkish lira:₺|TWD:New Taiwan dollar:NT$|UAH:Ukrainian hryvnia:₴|USD:United States (US) dollar:$|UYU:Uruguayan peso:$|VND:Vietnamese đồng:₫|XAF:Central African CFA franc:CFA|XOF:West African CFA franc:CFA|ZAR:South African rand:R"

type gooNamed struct{ Code, Name string }

type gooCurrency struct{ Code, Name, Symbol string }

var (
	gooCountries    map[string]string
	gooCountryOrder []gooNamed // sorted by name
	gooStates       map[string][]gooNamed
	gooContinents   []struct {
		Code, Name string
		Countries  []string
	}
	gooCurrencies    map[string]gooCurrency
	gooCurrencyOrder []gooCurrency
)

func init() {
	gooCountries = map[string]string{}
	for _, p := range strings.Split(gooCountryList, "|") {
		code, name, _ := strings.Cut(p, ":")
		gooCountries[code] = name
		gooCountryOrder = append(gooCountryOrder, gooNamed{code, name})
	}
	sort.Slice(gooCountryOrder, func(i, j int) bool { return gooCountryOrder[i].Name < gooCountryOrder[j].Name })
	gooStates = map[string][]gooNamed{}
	for _, p := range strings.Split(gooStateList, "|") {
		cc, list, _ := strings.Cut(p, ":")
		for _, st := range strings.Split(list, ";") {
			code, name, _ := strings.Cut(st, "=")
			gooStates[cc] = append(gooStates[cc], gooNamed{code, name})
		}
	}
	for _, p := range strings.Split(gooContinentList, "|") {
		f := strings.SplitN(p, ":", 3)
		gooContinents = append(gooContinents, struct {
			Code, Name string
			Countries  []string
		}{f[0], f[1], strings.Split(f[2], ",")})
	}
	gooCurrencies = map[string]gooCurrency{}
	for _, p := range strings.Split(gooCurrencyList, "|") {
		f := strings.SplitN(p, ":", 3)
		cur := gooCurrency{f[0], f[1], f[2]}
		gooCurrencies[cur.Code] = cur
		gooCurrencyOrder = append(gooCurrencyOrder, cur)
	}
	sort.Slice(gooCurrencyOrder, func(i, j int) bool { return gooCurrencyOrder[i].Name < gooCurrencyOrder[j].Name })
}

// gooCountryName is WC()->countries->countries[code], the code itself when unknown.
func gooCountryName(code string) string {
	if n, ok := gooCountries[code]; ok {
		return n
	}
	return code
}

// gooStateName is the state's name in a country, the code itself when the country has no list.
func gooStateName(country, state string) string {
	for _, s := range gooStates[country] {
		if s.Code == state {
			return s.Name
		}
	}
	return state
}

// gooContinentOf is the continent code of a country, "" when unknown.
func gooContinentOf(country string) string {
	for _, c := range gooContinents {
		for _, cc := range c.Countries {
			if cc == country {
				return c.Code
			}
		}
	}
	return ""
}

// gooCurrencySymbol is get_woocommerce_currency_symbol().
func gooCurrencySymbol(code string) string {
	if c, ok := gooCurrencies[code]; ok {
		return c.Symbol
	}
	return code
}
