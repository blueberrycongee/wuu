import XCTest

final class ConversationUITests: XCTestCase {
    @MainActor func testLoginSendStreamStopAndReconnect() throws {
        continueAfterFailure = false
        let server = try XCTUnwrap(ProcessInfo.processInfo.environment["WUU_NATIVE_UI_SERVER"])
        XCTAssertTrue(server.hasPrefix("http://127.0.0.1:"), "Run through clients/native/ui-test.py with an isolated fixture")
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        app.launch()
        XCTAssertTrue(app.buttons["设置连接服务器"].waitForExistence(timeout: 10))
        app.buttons["设置连接服务器"].tap()
        let address = app.textFields["https://你的服务器"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        address.tap(); address.typeText(server)
        app.buttons["完成"].tap()
        app.buttons["密码登录"].tap()
        app.textFields["用户名"].tap(); app.textFields["用户名"].typeText("native-test")
        app.secureTextFields["密码"].tap(); app.secureTextFields["密码"].typeText("native-test-password")
        app.buttons["登录"].tap()
        let computer = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "UI test computer")).firstMatch
        XCTAssertTrue(computer.waitForExistence(timeout: 15))
        // The system password sheet uses the simulator language, not the app's launch language.
        // Its service can launch after the account response on a cold simulator.
        let later = app.buttons.matching(NSPredicate(format: "label IN %@", ["以后", "Not Now"])).firstMatch
        if later.waitForExistence(timeout: 15) {
            expectation(for: NSPredicate(format: "hittable == true"), evaluatedWith: later)
            waitForExpectations(timeout: 5)
            later.tap()
            XCTAssertTrue(later.waitForNonExistence(timeout: 5))
        }
        // XCTest occasionally reports (-1, -1) for the iOS 26 toolbar hit point.
        // Tap its rendered center and verify the resulting sheet.
        app.navigationBars["你的电脑"].buttons["账号设置"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(app.buttons["通知"].waitForExistence(timeout: 5)); app.buttons["通知"].tap()
        let enablePush = app.buttons["开启或重新注册"]
        XCTAssertTrue(enablePush.waitForExistence(timeout: 5)); XCTAssertFalse(enablePush.isEnabled)
        app.navigationBars["通知"].buttons["账号设置"].tap()
        app.buttons["开源许可"].tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Wuu native mobile")).firstMatch.waitForExistence(timeout: 5))
        app.navigationBars["开源许可"].buttons["账号设置"].tap(); app.buttons["完成"].tap()
        computer.tap()
        let newThread = app.buttons["新会话"].firstMatch
        XCTAssertTrue(newThread.waitForExistence(timeout: 15))
        expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: newThread)
        waitForExpectations(timeout: 15); newThread.tap()
        let input = app.textFields["发送消息"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        input.tap(); input.typeText("ui-hello")
        app.buttons["发送"].tap()
        XCTAssertTrue(app.staticTexts["Received: ui-hello"].waitForExistence(timeout: 15))
        XCTAssertTrue(app.buttons["添加附件"].exists)
        input.tap(); input.typeText("ui-tools"); app.buttons["发送"].tap()
        XCTAssertTrue(app.staticTexts["Received: ui-tools"].waitForExistence(timeout: 15))
        let tool = app.buttons["tool-activity"].firstMatch
        XCTAssertTrue(tool.waitForExistence(timeout: 5)); tool.tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Native tool output")).firstMatch.waitForExistence(timeout: 5))
        tool.tap()
        input.tap(); input.typeText("ui-wait")
        app.buttons["发送"].tap()
        XCTAssertTrue(app.staticTexts["Streaming preview"].waitForExistence(timeout: 15))
        let stop = app.buttons["停止"]
        XCTAssertTrue(stop.waitForExistence(timeout: 5)); stop.tap()
        expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: stop)
        waitForExpectations(timeout: 15)
        XCUIDevice.shared.press(.home)
        app.activate()
        expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: app.buttons["添加附件"])
        waitForExpectations(timeout: 20)
        input.tap(); input.typeText("ui-return")
        app.buttons["发送"].tap()
        XCTAssertTrue(app.staticTexts["Received: ui-return"].waitForExistence(timeout: 15))
        app.buttons["会话操作"].tap(); app.buttons["会话设置"].tap()
        let permission = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "执行权限")).firstMatch
        XCTAssertTrue(permission.waitForExistence(timeout: 5)); permission.tap()
        app.buttons["只读"].tap(); app.buttons["保存"].tap()
        XCTAssertTrue(app.buttons["会话操作"].waitForExistence(timeout: 10))
        app.buttons["会话操作"].tap(); app.buttons["会话设置"].tap()
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", "执行权限", "只读")).firstMatch.waitForExistence(timeout: 5))
        app.buttons["取消"].tap()
        app.buttons["会话列表"].tap()
        let closeList = app.buttons["关闭会话列表"].firstMatch
        XCTAssertTrue(closeList.waitForExistence(timeout: 5))
        closeList.tap()
        XCTAssertTrue(app.staticTexts["Received: ui-return"].exists)
        let screenshot = XCTAttachment(screenshot: app.screenshot()); screenshot.lifetime = .keepAlways; add(screenshot)
        app.terminate()
    }
}
