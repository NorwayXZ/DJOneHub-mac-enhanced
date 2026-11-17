import SwiftUI

/// Country codes for the dial pad: pick the country, type the number as people
/// there write it, and the leading trunk 0 is dropped and the code added, so
/// "030 1234567" with Germany dials +49301234567.
struct DialCountry: Identifiable, Hashable {
    let iso: String
    let name: String
    let code: String
    var id: String { iso }

    var flag: String {
        iso.unicodeScalars.compactMap { UnicodeScalar(127397 + $0.value) }.map(String.init).joined()
    }

    static let all: [DialCountry] = [
        ("CN", "中国", "86"), ("HK", "中国香港", "852"), ("MO", "中国澳门", "853"), ("TW", "中国台湾", "886"),
        ("US", "美国", "1"), ("CA", "加拿大", "1"), ("GB", "英国", "44"), ("DE", "德国", "49"), ("FR", "法国", "33"),
        ("IT", "意大利", "39"), ("ES", "西班牙", "34"), ("NL", "荷兰", "31"), ("BE", "比利时", "32"), ("CH", "瑞士", "41"),
        ("AT", "奥地利", "43"), ("IE", "爱尔兰", "353"), ("PT", "葡萄牙", "351"), ("SE", "瑞典", "46"), ("NO", "挪威", "47"),
        ("DK", "丹麦", "45"), ("FI", "芬兰", "358"), ("PL", "波兰", "48"), ("CZ", "捷克", "420"), ("GR", "希腊", "30"),
        ("HU", "匈牙利", "36"), ("RO", "罗马尼亚", "40"), ("TR", "土耳其", "90"), ("RU", "俄罗斯", "7"), ("UA", "乌克兰", "380"),
        ("JP", "日本", "81"), ("KR", "韩国", "82"), ("SG", "新加坡", "65"), ("MY", "马来西亚", "60"), ("TH", "泰国", "66"),
        ("VN", "越南", "84"), ("PH", "菲律宾", "63"), ("ID", "印度尼西亚", "62"), ("KH", "柬埔寨", "855"), ("MM", "缅甸", "95"),
        ("LA", "老挝", "856"), ("MN", "蒙古", "976"), ("IN", "印度", "91"), ("PK", "巴基斯坦", "92"), ("BD", "孟加拉国", "880"),
        ("NP", "尼泊尔", "977"), ("LK", "斯里兰卡", "94"), ("AE", "阿联酋", "971"), ("SA", "沙特阿拉伯", "966"), ("QA", "卡塔尔", "974"),
        ("KW", "科威特", "965"), ("IL", "以色列", "972"), ("KZ", "哈萨克斯坦", "7"), ("AU", "澳大利亚", "61"), ("NZ", "新西兰", "64"),
        ("BR", "巴西", "55"), ("MX", "墨西哥", "52"), ("AR", "阿根廷", "54"), ("CL", "智利", "56"), ("CO", "哥伦比亚", "57"),
        ("PE", "秘鲁", "51"), ("ZA", "南非", "27"), ("EG", "埃及", "20"), ("NG", "尼日利亚", "234"), ("KE", "肯尼亚", "254"), ("MA", "摩洛哥", "212"),
    ].map { DialCountry(iso: $0.0, name: $0.1, code: $0.2) }

    static func find(_ iso: String) -> DialCountry? { all.first { $0.iso == iso } }

    /// What the module dials for what was typed. A full number (+ or 00),
    /// a code with * or #, and short local numbers (110, 10086) go out as typed.
    static func international(_ raw: String, country iso: String) -> String {
        let typed = raw.trimmingCharacters(in: .whitespaces).filter { !" -().".contains($0) }
        guard let country = find(iso), !typed.isEmpty,
              !typed.contains("*"), !typed.contains("#"), !typed.hasPrefix("+") else { return typed }
        if typed.hasPrefix("00") { return "+" + typed.dropFirst(2) }
        var national = typed.filter(\.isNumber)
        if national.count <= 5 { return typed }
        if country.code == "7", national.count == 11, national.hasPrefix("8") {
            national.removeFirst()
        } else if country.code != "39" {
            while national.hasPrefix("0") { national.removeFirst() }
        }
        return "+" + country.code + national
    }
}

struct DialCountryPicker: View {
    @Environment(\.dismiss) private var dismiss
    @Binding var selection: String
    @State private var query = ""

    private var matches: [DialCountry] {
        let q = query.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "+"))
        guard !q.isEmpty else { return DialCountry.all }
        return DialCountry.all.filter { $0.name.contains(q) || $0.code.hasPrefix(q) || $0.iso.caseInsensitiveCompare(q) == .orderedSame }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(L10n.t("拨打哪个国家/地区"))
                .font(.title3.weight(.semibold))
            TextField(L10n.t("搜索国家或区号"), text: $query)
                .textFieldStyle(.roundedBorder)
            List {
                if query.isEmpty {
                    row(flag: "☎️", name: L10n.t("不加国家码（号码原样拨出）"), code: "", iso: "")
                }
                ForEach(matches) { country in
                    row(flag: country.flag, name: country.name, code: "+" + country.code, iso: country.iso)
                }
            }
            .frame(height: 340)
            HStack {
                Spacer()
                Button(L10n.t("取消")) { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }
        }
        .frame(width: 340)
        .padding(20)
    }

    private func row(flag: String, name: String, code: String, iso: String) -> some View {
        Button {
            selection = iso
            dismiss()
        } label: {
            HStack(spacing: 10) {
                Text(flag).font(.title3)
                Text(name)
                Spacer()
                Text(code).foregroundStyle(.secondary).monospacedDigit()
                if selection == iso {
                    Image(systemName: "checkmark").foregroundStyle(Color.accentColor)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}
