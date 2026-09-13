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
        let input = app.descendants(matching: .any)["harness-composer"]
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
        try verifyCollaboration(app)
        app.terminate()
    }

    @MainActor private func verifyCollaboration(_ app: XCUIApplication) throws {
        let harnessInput = app.descendants(matching: .any)["harness-composer"]
        harnessInput.tap(); harnessInput.typeText("harness-draft")
        dismissKeyboard(app)
        tapCenter(app.tabBars.buttons["协作"])
        let room = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Native collaboration")).firstMatch
        XCTAssertTrue(room.waitForExistence(timeout: 15))
        room.tap()
        let input = app.descendants(matching: .any)["collaboration-composer"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        input.tap(); input.typeText("collaboration-draft")
        dismissKeyboard(app)
        tapCenter(app.tabBars.buttons["会话"])
        XCTAssertEqual(harnessInput.value as? String, "harness-draft")
        tapCenter(app.tabBars.buttons["协作"])
        XCTAssertEqual(input.value as? String, "collaboration-draft")
        app.buttons["发送协作消息"].tap()
        XCTAssertTrue(app.staticTexts["collaboration-draft"].waitForExistence(timeout: 10))
        tapCenter(app.buttons["成员与任务"])
        XCTAssertTrue(app.staticTexts["Alpha"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Beta"].exists)
        capture(app, name: "collaboration-members-tasks")
        tapCenter(app.buttons["完成"])
        capture(app, name: "collaboration-room")
        tapCenter(app.buttons["协作列表"])
        tapCenter(app.buttons["新协作对话"])
        app.buttons["Alpha"].tap()
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        XCTAssertTrue(app.navigationBars["Alpha"].exists)
        tapCenter(app.buttons["协作列表"])
        tapCenter(app.buttons["新协作对话"])
        tapCenter(app.switches["创建群聊"].switches.firstMatch)
        XCTAssertTrue(app.textFields["群聊名称"].waitForExistence(timeout: 5))
        app.textFields["群聊名称"].tap(); app.textFields["群聊名称"].typeText("Phone team")
        app.buttons["Alpha"].tap(); app.buttons["Beta"].tap()
        tapCenter(app.buttons["创建"])
        XCTAssertTrue(app.navigationBars["Phone team"].waitForExistence(timeout: 10))
        input.tap(); input.typeText("new-room-draft")
        XCUIDevice.shared.press(.home); app.activate()
        XCTAssertEqual(input.value as? String, "new-room-draft")
        expectation(for: NSPredicate(format: "enabled == true"), evaluatedWith: app.buttons["发送协作消息"])
        waitForExpectations(timeout: 20)
        dismissKeyboard(app)
        tapCenter(app.buttons["协作列表"])
        capture(app, name: "collaboration-directory")
    }

    @MainActor private func capture(_ app: XCUIApplication, name: String) {
        let image = XCTAttachment(screenshot: app.screenshot())
        image.name = name; image.lifetime = .keepAlways; add(image)
    }

    @MainActor private func tapCenter(_ element: XCUIElement) {
        XCTAssertTrue(element.waitForExistence(timeout: 5))
        element.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
    }

    @MainActor private func dismissKeyboard(_ app: XCUIApplication) {
        // A fresh simulator can show the system's slide-to-type onboarding
        // above the app keyboard. Dismiss it before touching app controls.
        let onboarding = app.buttons.matching(NSPredicate(format: "label IN %@", ["Continue", "继续"])).firstMatch
        if onboarding.exists { tapCenter(onboarding) }
        tapCenter(app.buttons["收起键盘"])
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5))
    }
}
